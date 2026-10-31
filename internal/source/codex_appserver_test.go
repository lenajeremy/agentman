package source

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeCodexDaemon stands in for Codex's shared app-server: WebSocket on a
// unix socket, one fakeCodexConn per client connection.
type fakeCodexDaemon struct {
	socket string
	conns  chan *fakeCodexConn
}

type fakeCodexConn struct {
	ws *websocket.Conn
	in chan codexRPCMessage
}

func startFakeCodexDaemon(t *testing.T) *fakeCodexDaemon {
	t.Helper()
	// A unix socket path is limited to about 104 bytes, which t.TempDir()
	// on macOS already comes close to.
	dir, err := os.MkdirTemp("", "cx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	fake := &fakeCodexDaemon{socket: filepath.Join(dir, "d.sock"), conns: make(chan *fakeCodexConn, 8)}
	listener, err := net.Listen("unix", fake.socket)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var open []*websocket.Conn
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		open = append(open, ws)
		mu.Unlock()
		conn := &fakeCodexConn{ws: ws, in: make(chan codexRPCMessage, 64)}
		go func() {
			defer close(conn.in)
			for {
				_, data, err := ws.Read(context.Background())
				if err != nil {
					return
				}
				var msg codexRPCMessage
				if json.Unmarshal(data, &msg) == nil {
					conn.in <- msg
				}
			}
		}()
		fake.conns <- conn
	})}
	go server.Serve(listener)
	t.Cleanup(func() {
		server.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, ws := range open {
			ws.CloseNow()
		}
	})
	return fake
}

func (f *fakeCodexDaemon) accept(t *testing.T) *fakeCodexConn {
	t.Helper()
	select {
	case conn := <-f.conns:
		return conn
	case <-time.After(5 * time.Second):
		t.Fatal("the client never connected")
		return nil
	}
}

func (c *fakeCodexConn) next(t *testing.T) codexRPCMessage {
	t.Helper()
	select {
	case msg, ok := <-c.in:
		if !ok {
			t.Fatal("the client hung up")
		}
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("the client sent nothing")
		return codexRPCMessage{}
	}
}

func (c *fakeCodexConn) expect(t *testing.T, method string) codexRPCMessage {
	t.Helper()
	msg := c.next(t)
	if msg.Method != method {
		t.Fatalf("client sent %q, want %q", msg.Method, method)
	}
	return msg
}

// quiet checks the client sends nothing for a moment.
func (c *fakeCodexConn) quiet(t *testing.T) {
	t.Helper()
	select {
	case msg := <-c.in:
		t.Fatalf("client sent %+v unprompted", msg)
	case <-time.After(150 * time.Millisecond):
	}
}

func (c *fakeCodexConn) send(t *testing.T, message any) {
	t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ws.Write(context.Background(), websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func (c *fakeCodexConn) reply(t *testing.T, request codexRPCMessage, result any) {
	t.Helper()
	c.send(t, map[string]any{"id": request.ID, "result": result})
}

// handshake plays the daemon's side of initialize.
func (c *fakeCodexConn) handshake(t *testing.T) codexRPCMessage {
	t.Helper()
	init := c.expect(t, "initialize")
	c.reply(t, init, map[string]any{"codexHome": "/h/.codex", "platformFamily": "unix", "platformOs": "macos"})
	c.expect(t, "initialized")
	return init
}

func newTestCodexAppServer(t *testing.T, socket string) *codexAppServer {
	t.Helper()
	s := newCodexAppServer(socket, "test")
	s.retryMin, s.retryMax = 10*time.Millisecond, 40*time.Millisecond
	return s
}

func runCodexAppServer(t *testing.T, s *codexAppServer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func waitConnected(t *testing.T, s *codexAppServer) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !s.connected() {
		if time.Now().After(deadline) {
			t.Fatal("never connected")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// callAsync makes a call on its own goroutine, so the fake can answer it.
func callAsync(s *codexAppServer, method string, params, dest any) <-chan error {
	result := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result <- s.call(ctx, method, params, dest)
	}()
	return result
}

func waitErr(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the call never returned")
		return nil
	}
}

func TestCodexAppServerIntroducesItselfAndCalls(t *testing.T) {
	fake := startFakeCodexDaemon(t)
	s := newTestCodexAppServer(t, fake.socket)
	runCodexAppServer(t, s)

	conn := fake.accept(t)
	init := conn.handshake(t)
	var params struct {
		ClientInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
		Capabilities struct {
			ExperimentalAPI bool     `json:"experimentalApi"`
			OptOut          []string `json:"optOutNotificationMethods"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(init.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.ClientInfo.Name != "agentman" || params.ClientInfo.Version != "test" {
		t.Fatalf("clientInfo = %+v", params.ClientInfo)
	}
	if !params.Capabilities.ExperimentalAPI {
		t.Fatal("experimentalApi not requested: plan mode and steering need it")
	}
	if !slices.Contains(params.Capabilities.OptOut, "turn/moderationMetadata") {
		t.Fatalf("opt-outs = %v", params.Capabilities.OptOut)
	}
	waitConnected(t, s)

	var loaded struct {
		Data []string `json:"data"`
	}
	result := callAsync(s, "thread/loaded/list", map[string]any{}, &loaded)
	request := conn.expect(t, "thread/loaded/list")
	conn.reply(t, request, map[string]any{"data": []string{"t1", "t2"}, "nextCursor": nil})
	if err := waitErr(t, result); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(loaded.Data, []string{"t1", "t2"}) {
		t.Fatalf("loaded = %v", loaded.Data)
	}
}

func TestCodexAppServerReportsTheDaemonsRefusal(t *testing.T) {
	fake := startFakeCodexDaemon(t)
	s := newTestCodexAppServer(t, fake.socket)
	runCodexAppServer(t, s)
	conn := fake.accept(t)
	conn.handshake(t)
	waitConnected(t, s)

	result := callAsync(s, "thread/resume", codexResumeParams("t1"), nil)
	request := conn.expect(t, "thread/resume")
	conn.send(t, map[string]any{"id": request.ID, "error": map[string]any{"code": -32600, "message": "no rollout found for thread id t1"}})
	err := waitErr(t, result)
	var refused *codexRPCError
	if !errors.As(err, &refused) || refused.Message != "no rollout found for thread id t1" {
		t.Fatalf("err = %v, want the daemon's refusal", err)
	}
}

// Requests from the daemon are offered to every client, the terminal first
// among them, so the client must pass them on and never answer one itself.
func TestCodexAppServerPassesRequestsOnAndAnswersOnlyWhenTold(t *testing.T) {
	fake := startFakeCodexDaemon(t)
	s := newTestCodexAppServer(t, fake.socket)
	got := make(chan codexRPCMessage, 4)
	s.handle = func(msg codexRPCMessage) { got <- msg }
	runCodexAppServer(t, s)
	conn := fake.accept(t)
	conn.handshake(t)
	waitConnected(t, s)

	conn.send(t, map[string]any{"method": "thread/status/changed", "params": map[string]any{"threadId": "t1", "status": map[string]any{"type": "active"}}})
	conn.send(t, map[string]any{"id": 9, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "t1", "command": "echo hi > note.txt"}})

	var messages []codexRPCMessage
	for len(messages) < 2 {
		select {
		case msg := <-got:
			messages = append(messages, msg)
		case <-time.After(5 * time.Second):
			t.Fatalf("handled %d of 2", len(messages))
		}
	}
	if messages[0].Method != "thread/status/changed" || messages[0].serverRequest() {
		t.Fatalf("first = %+v, want the notification", messages[0])
	}
	if messages[1].Method != "item/commandExecution/requestApproval" || !messages[1].serverRequest() {
		t.Fatalf("second = %+v, want the approval request", messages[1])
	}
	conn.quiet(t)

	if err := s.respond(messages[1].ID, map[string]any{"decision": "accept"}); err != nil {
		t.Fatal(err)
	}
	answer := conn.next(t)
	var result struct {
		Decision string `json:"decision"`
	}
	if string(answer.ID) != "9" || json.Unmarshal(answer.Result, &result) != nil || result.Decision != "accept" {
		t.Fatalf("answer = %+v", answer)
	}
}

// The daemon updates itself by restarting on the same socket without a close
// frame. The client comes back on its own and joins its threads again.
func TestCodexAppServerRejoinsAfterTheDaemonRestarts(t *testing.T) {
	fake := startFakeCodexDaemon(t)
	s := newTestCodexAppServer(t, fake.socket)
	rejoined := make(chan string, 4)
	s.rejoined = func(id string) { rejoined <- id }
	runCodexAppServer(t, s)
	conn := fake.accept(t)
	conn.handshake(t)
	waitConnected(t, s)

	var thread struct {
		ID string `json:"id"`
	}
	result := make(chan error, 1)
	go func() { result <- s.join(context.Background(), "t1", &thread) }()
	resume := conn.expect(t, "thread/resume")
	var params map[string]any
	json.Unmarshal(resume.Params, &params)
	if params["threadId"] != "t1" || params["excludeTurns"] != true {
		t.Fatalf("resume params = %v", params)
	}
	conn.reply(t, resume, map[string]any{"thread": map[string]any{"id": "t1"}})
	if err := waitErr(t, result); err != nil || thread.ID != "t1" {
		t.Fatalf("join = %v, thread %+v", err, thread)
	}

	conn.ws.CloseNow()
	again := fake.accept(t)
	again.handshake(t)
	resume = again.expect(t, "thread/resume")
	json.Unmarshal(resume.Params, &params)
	if params["threadId"] != "t1" {
		t.Fatalf("rejoined %v, want t1", params["threadId"])
	}
	again.reply(t, resume, map[string]any{"thread": map[string]any{"id": "t1"}})
	select {
	case id := <-rejoined:
		if id != "t1" {
			t.Fatalf("rejoined %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("never told about the rejoin")
	}
}

// A thread deleted while the connection was down cannot be joined again, and
// asking for it on every reconnect would never stop failing.
func TestCodexAppServerForgetsAThreadItCannotRejoin(t *testing.T) {
	fake := startFakeCodexDaemon(t)
	s := newTestCodexAppServer(t, fake.socket)
	runCodexAppServer(t, s)
	conn := fake.accept(t)
	conn.handshake(t)
	waitConnected(t, s)
	for _, id := range []string{"t1", "t2"} {
		result := make(chan error, 1)
		go func() { result <- s.join(context.Background(), id, nil) }()
		conn.reply(t, conn.expect(t, "thread/resume"), map[string]any{"thread": map[string]any{"id": id}})
		if err := waitErr(t, result); err != nil {
			t.Fatal(err)
		}
	}

	conn.ws.CloseNow()
	second := fake.accept(t)
	second.handshake(t)
	gone := second.expect(t, "thread/resume")
	second.send(t, map[string]any{"id": gone.ID, "error": map[string]any{"code": -32600, "message": "no rollout found for thread id t1"}})
	second.reply(t, second.expect(t, "thread/resume"), map[string]any{"thread": map[string]any{"id": "t2"}})
	second.quiet(t)

	second.ws.CloseNow()
	third := fake.accept(t)
	third.handshake(t)
	resume := third.expect(t, "thread/resume")
	var params map[string]any
	json.Unmarshal(resume.Params, &params)
	if params["threadId"] != "t2" {
		t.Fatalf("rejoined %v, want only t2", params["threadId"])
	}
	third.reply(t, resume, map[string]any{"thread": map[string]any{"id": "t2"}})
	third.quiet(t)
}

func TestCodexAppServerFailsCallsWhileDown(t *testing.T) {
	s := newTestCodexAppServer(t, filepath.Join(t.TempDir(), "missing.sock"))
	runCodexAppServer(t, s)
	if err := s.call(context.Background(), "thread/loaded/list", nil, nil); !errors.Is(err, errCodexAppServerDown) {
		t.Fatalf("call with no daemon = %v, want errCodexAppServerDown", err)
	}
	if err := s.join(context.Background(), "t1", nil); !errors.Is(err, errCodexAppServerDown) {
		t.Fatalf("join with no daemon = %v, want errCodexAppServerDown", err)
	}
	s.mu.Lock()
	remembered := len(s.joined)
	s.mu.Unlock()
	if remembered != 0 {
		t.Fatal("a failed join was remembered")
	}
}

// A call in flight when the daemon restarts must not wait for an answer that
// will never come.
func TestCodexAppServerFailsACallInFlightWhenTheConnectionDrops(t *testing.T) {
	fake := startFakeCodexDaemon(t)
	s := newTestCodexAppServer(t, fake.socket)
	runCodexAppServer(t, s)
	conn := fake.accept(t)
	conn.handshake(t)
	waitConnected(t, s)

	result := callAsync(s, "thread/turns/list", map[string]any{"threadId": "t1"}, nil)
	conn.expect(t, "thread/turns/list")
	conn.ws.CloseNow()
	if err := waitErr(t, result); !errors.Is(err, errCodexAppServerDown) {
		t.Fatalf("err = %v, want errCodexAppServerDown", err)
	}
	fake.accept(t).handshake(t)
	waitConnected(t, s)
}
