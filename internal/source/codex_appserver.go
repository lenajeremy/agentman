package source

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Codex 0.164 runs a shared app-server daemon, and every terminal Codex is a
// client of it: the TUI draws what the daemon reports and sends what is typed
// to it. Agentman connects to the same daemon as one more client. Asked to
// resume a thread that is already running, the daemon rejoins that thread
// rather than loading a copy, so a session open in a terminal can be followed
// and driven live. Checked against Codex 0.166 on 2026-10-31, with a terminal
// attached to the same thread throughout:
//
//   - the reply streams in word by word (item/agentMessage/delta);
//   - a turn started from here shows in the terminal as if it had been typed,
//     and a message sent during a turn (turn/steer) joins that turn;
//   - approvals and questions reach every client as server requests, and
//     answering one here clears the terminal's prompt (serverRequest/resolved);
//   - turn/interrupt stops a turn, and the terminal says so.
//
// The protocol is JSON-RPC (without the "jsonrpc" member) over a WebSocket on
// a unix socket, which the daemon links from
// $CODEX_HOME/app-server-control/app-server-control.sock. The socket only
// speaks WebSocket: a plain JSON line makes the daemon drop the connection.
//
// The daemon updates itself when Codex does. It restarts on the same socket
// without a close frame, dropping every client, so this connection redials on
// its own and rejoins every thread it had joined. While it is down a call
// fails at once with errCodexAppServerDown, and callers use the rollout path.
//
// OpenAI marks the protocol experimental. Everything that knows its shape
// lives in the codex_appserver files, so a change breaks in one place.

const (
	// codexAppServerDialTimeout bounds connecting and the WebSocket handshake.
	// The daemon is local: it answers at once or is not there.
	codexAppServerDialTimeout = 3 * time.Second
	// codexAppServerInitTimeout bounds the initialize exchange.
	codexAppServerInitTimeout = 10 * time.Second
	// codexAppServerWriteTimeout bounds one message written to the daemon, so
	// a wedged daemon cannot hold a caller forever.
	codexAppServerWriteTimeout = 5 * time.Second
	// codexAppServerReadLimit bounds one message from the daemon. The daemon
	// advertises 16 MiB for an unfragmented one; a resumed thread or a big
	// command output can come close, and a fragmented message can be larger.
	codexAppServerReadLimit = 64 << 20
	// codexAppServerSteady is how long a connection must have stayed up for
	// a drop to count as a restart rather than a refusal, and the retry delay
	// to start again from its shortest.
	codexAppServerSteady = 5 * time.Second
)

// errCodexAppServerDown reports that there is no connection to the daemon
// right now: Codex is not installed, too old to run one, not started yet, or
// restarting after an update.
var errCodexAppServerDown = errors.New("source: Codex's app-server is not reachable")

// codexAppServerOptOut names notifications Agentman never reads. The daemon
// sends them to every client otherwise; moderation metadata alone is several
// kilobytes a turn.
var codexAppServerOptOut = []string{
	"turn/moderationMetadata",
	"rawResponseItem/completed",
	"rawResponse/completed",
}

// codexAppServerSocket is where the daemon's socket is linked from.
func codexAppServerSocket(home string) string {
	return filepath.Join(home, ".codex", "app-server-control", "app-server-control.sock")
}

// codexRPCError is an error the daemon answered a call with.
type codexRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *codexRPCError) Error() string { return "codex app-server: " + e.Message }

// codexRPCMessage is one message in either direction. A method with an id is
// a request from the daemon (an approval, a question), which waits for an
// answer; a method alone is a notification; an id alone is the answer to one
// of our calls.
type codexRPCMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *codexRPCError  `json:"error,omitempty"`
}

// serverRequest reports whether the message waits for an answer.
func (m codexRPCMessage) serverRequest() bool { return m.Method != "" && len(m.ID) > 0 }

// codexRPCReply is what a waiting call is handed: the daemon's answer, or
// the reason none will come.
type codexRPCReply struct {
	msg codexRPCMessage
	err error
}

// codexAppServer is Agentman's one connection to Codex's shared daemon.
//
// A request from the daemon is offered to every client, the terminal included,
// and the first answer settles it. Nothing here answers one on its own: only
// an answer the user gave through Agentman goes back, and anything else is
// left for the terminal.
type codexAppServer struct {
	socket  string
	version string
	client  *http.Client

	// handle receives every notification and request from the daemon, on
	// the connection's read goroutine, in the order they arrived. It must not
	// block.
	handle func(codexRPCMessage)
	// rejoined is told about each thread joined again after a reconnect.
	// Whatever happened while the connection was down was not seen, so a
	// follower reads the thread again.
	rejoined func(threadID string)
	// up runs, on a goroutine of its own, each time a connection is ready
	// for calls; down runs each time one drops.
	up, down func()

	// retryMin and retryMax bound the delay between connection attempts.
	retryMin, retryMax time.Duration

	mu      sync.Mutex
	conn    *websocket.Conn // nil while down
	nextID  int64
	pending map[int64]chan codexRPCReply
	// joined is every thread a caller asked to follow, to be rejoined after
	// a reconnect.
	joined map[string]bool
}

func newCodexAppServer(socket, version string) *codexAppServer {
	s := &codexAppServer{
		socket:   socket,
		version:  version,
		retryMin: 500 * time.Millisecond,
		retryMax: 10 * time.Second,
		pending:  map[int64]chan codexRPCReply{},
		joined:   map[string]bool{},
	}
	s.client = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", s.socket)
		},
	}}
	return s
}

// run keeps the connection up until ctx is cancelled.
func (s *codexAppServer) run(ctx context.Context) {
	delay := s.retryMin
	for ctx.Err() == nil {
		if conn, err := s.dial(ctx); err == nil {
			started := time.Now()
			s.serve(ctx, conn)
			if time.Since(started) >= codexAppServerSteady {
				delay = s.retryMin
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(delay*2, s.retryMax)
	}
}

// connected reports whether the daemon is reachable right now.
func (s *codexAppServer) connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn != nil
}

func (s *codexAppServer) dial(ctx context.Context) (*websocket.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, codexAppServerDialTimeout)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, "ws://localhost/", &websocket.DialOptions{HTTPClient: s.client})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(codexAppServerReadLimit)
	return conn, nil
}

// serve runs one connection until it drops.
func (s *codexAppServer) serve(ctx context.Context, conn *websocket.Conn) {
	// The answer to initialize arrives through the read loop, so it starts
	// first.
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.read(ctx, conn)
	}()
	defer func() {
		conn.CloseNow()
		<-done
		s.dropped(conn)
	}()

	if err := s.initialize(ctx, conn); err != nil {
		return
	}
	s.mu.Lock()
	s.conn = conn
	threads := make([]string, 0, len(s.joined))
	for id := range s.joined {
		threads = append(threads, id)
	}
	s.mu.Unlock()
	sort.Strings(threads)
	go s.rejoin(ctx, conn, threads)
	if s.up != nil {
		go s.up()
	}

	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (s *codexAppServer) initialize(ctx context.Context, conn *websocket.Conn) error {
	initCtx, cancel := context.WithTimeout(ctx, codexAppServerInitTimeout)
	defer cancel()
	params := map[string]any{
		"clientInfo": map[string]any{"name": "agentman", "title": "Agentman", "version": s.version},
		"capabilities": map[string]any{
			// Plan mode (collaborationMode on turn/start) and turn/steer
			// are behind it.
			"experimentalApi":           true,
			"requestAttestation":        false,
			"optOutNotificationMethods": codexAppServerOptOut,
		},
	}
	if err := s.callOn(initCtx, conn, "initialize", params, nil); err != nil {
		return err
	}
	return s.writeOn(conn, map[string]any{"method": "initialized"})
}

// rejoin joins each thread again on a new connection. One that cannot be
// joined any more (deleted, or gone from disk) is forgotten.
func (s *codexAppServer) rejoin(ctx context.Context, conn *websocket.Conn, threads []string) {
	for _, id := range threads {
		err := s.callOn(ctx, conn, "thread/resume", codexResumeParams(id), nil)
		var refused *codexRPCError
		switch {
		case err == nil:
			if s.rejoined != nil {
				s.rejoined(id)
			}
		case errors.As(err, &refused):
			s.mu.Lock()
			delete(s.joined, id)
			s.mu.Unlock()
		default:
			// The connection went too; the next one tries again.
			return
		}
	}
}

func (s *codexAppServer) read(ctx context.Context, conn *websocket.Conn) {
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if kind != websocket.MessageText {
			continue
		}
		var msg codexRPCMessage
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		if msg.Method != "" {
			if s.handle != nil {
				s.handle(msg)
			}
			continue
		}
		var id int64
		if json.Unmarshal(msg.ID, &id) != nil {
			continue
		}
		s.mu.Lock()
		reply := s.pending[id]
		delete(s.pending, id)
		s.mu.Unlock()
		if reply != nil {
			reply <- codexRPCReply{msg: msg}
		}
	}
}

// dropped forgets a connection that has gone, and fails every call still
// waiting on it.
func (s *codexAppServer) dropped(conn *websocket.Conn) {
	s.mu.Lock()
	wasUp := s.conn == conn
	if wasUp {
		s.conn = nil
	}
	for id, reply := range s.pending {
		delete(s.pending, id)
		reply <- codexRPCReply{err: errCodexAppServerDown}
	}
	s.mu.Unlock()
	if wasUp && s.down != nil {
		s.down()
	}
}

// call sends a request and waits for its answer, decoding the result into
// dest when dest is not nil.
func (s *codexAppServer) call(ctx context.Context, method string, params, dest any) error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return errCodexAppServerDown
	}
	return s.callOn(ctx, conn, method, params, dest)
}

func (s *codexAppServer) callOn(ctx context.Context, conn *websocket.Conn, method string, params, dest any) error {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	reply := make(chan codexRPCReply, 1)
	s.pending[id] = reply
	s.mu.Unlock()
	forget := func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}

	request := map[string]any{"id": id, "method": method}
	if params != nil {
		request["params"] = params
	}
	if err := s.writeOn(conn, request); err != nil {
		forget()
		return errCodexAppServerDown
	}
	select {
	case answer := <-reply:
		switch {
		case answer.err != nil:
			return answer.err
		case answer.msg.Error != nil:
			return answer.msg.Error
		case dest != nil && len(answer.msg.Result) > 0:
			return json.Unmarshal(answer.msg.Result, dest)
		}
		return nil
	case <-ctx.Done():
		forget()
		return ctx.Err()
	}
}

// respond answers a request from the daemon.
func (s *codexAppServer) respond(id json.RawMessage, result any) error {
	return s.send(map[string]any{"id": id, "result": result})
}

func (s *codexAppServer) send(message any) error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return errCodexAppServerDown
	}
	return s.writeOn(conn, message)
}

func (s *codexAppServer) writeOn(conn *websocket.Conn, message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexAppServerWriteTimeout)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, data)
}

// join follows a thread: from now on its notifications and requests reach
// handle, including after a reconnect. A thread with nothing sent to it yet
// cannot be joined, because the daemon finds threads by their rollout and
// Codex writes that with the first message. thread, when not nil, receives
// the thread as the daemon describes it.
func (s *codexAppServer) join(ctx context.Context, threadID string, thread any) error {
	var reply struct {
		Thread json.RawMessage `json:"thread"`
	}
	if err := s.call(ctx, "thread/resume", codexResumeParams(threadID), &reply); err != nil {
		return err
	}
	s.mu.Lock()
	s.joined[threadID] = true
	s.mu.Unlock()
	if thread != nil && len(reply.Thread) > 0 {
		return json.Unmarshal(reply.Thread, thread)
	}
	return nil
}

// adopted records a thread this connection joined by starting it, so it is
// rejoined after a reconnect and left like one joined with join.
func (s *codexAppServer) adopted(threadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.joined[threadID] = true
}

// leave stops following a thread. The thread itself carries on.
func (s *codexAppServer) leave(ctx context.Context, threadID string) error {
	s.mu.Lock()
	delete(s.joined, threadID)
	s.mu.Unlock()
	return s.call(ctx, "thread/unsubscribe", map[string]any{"threadId": threadID}, nil)
}

// codexResumeParams joins a thread without its history, which comes from
// thread/turns/list a page at a time instead.
func codexResumeParams(threadID string) map[string]any {
	return map[string]any{"threadId": threadID, "excludeTurns": true}
}
