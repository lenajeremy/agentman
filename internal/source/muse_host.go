package source

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Agentman's one connection to Meta Muse: a `muse serve --join` child
// speaking MSP, Muse's JSON-RPC 2.0 protocol, one message per line on its
// stdin and stdout. With --join it goes through the machine's Muse daemon
// when one runs, so sessions there (a terminal started as a daemon client
// among them) can be watched live; with none it hosts sessions itself. Either
// way session/list sees every session Muse has kept.
//
// Muse lets one connection drive a session at a time, so Agentman never holds
// one longer than it needs to: it takes a session to run a turn and lets go
// when the turn ends (muse.go).

const (
	museCallTimeout    = 20 * time.Second
	museConnectTimeout = 8 * time.Second
	// museLineLimit bounds one MSP message: a long history read is large.
	museLineLimit = 64 << 20
	// museRetryMax is the longest wait between restarts of the child.
	museRetryMax = 30 * time.Second
	// museClientName identifies Agentman to Muse; it must match [a-z0-9_]+.
	museClientName = "agentman"
)

var errMuseDown = errors.New("source: Muse is not reachable")

// museRPCError is an error Muse answered a request with.
type museRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Kind string `json:"kind"`
	} `json:"data"`
}

func (e *museRPCError) Error() string { return "muse: " + e.Message }

// museInUse reports Muse refusing because another connection — a terminal,
// usually — is driving the session.
func museInUse(err error) bool {
	var rpc *museRPCError
	return errors.As(err, &rpc) && rpc.Data.Kind == "sessionInUse"
}

// museMessage is one MSP message: a reply (ID set, Method empty), a
// notification (Method set, no ID), or a request from Muse (both set).
type museMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *museRPCError   `json:"error,omitempty"`
}

// museTransport starts one connection: what to write to, what to read from,
// how to end it, and the process it runs in (0 for none). Tests replace it
// with an in-memory host.
type museTransport func(ctx context.Context) (io.WriteCloser, io.Reader, func(), int, error)

type museHost struct {
	start museTransport

	// child is the `muse serve` process of the current connection. With no
	// daemon running it hosts what Agentman takes, and lists those sessions
	// in Muse's live registry as a terminal would; they are not a terminal's.
	child atomic.Int64

	mu        sync.Mutex
	conn      *museConn
	up        chan struct{} // closed while connected; replaced on each drop
	listeners map[int]func(museMessage)
	nextKey   int
	// views are the sessions being watched (view/subscribe), each counted by
	// its watchers, so a reconnect watches them again.
	views map[string]int
}

type museConn struct {
	in      io.WriteCloser
	writeMu sync.Mutex
	nextID  atomic.Int64
	pending sync.Map // id string → chan museMessage
}

func newMuseHost(start museTransport) *museHost {
	return &museHost{
		start:     start,
		up:        make(chan struct{}),
		listeners: map[int]func(museMessage){},
		views:     map[string]int{},
	}
}

// museServe starts `muse serve --join`, or reports that Muse is not
// installed.
func museServe(ctx context.Context) (io.WriteCloser, io.Reader, func(), int, error) {
	binary, err := museBinary()
	if err != nil {
		return nil, nil, nil, 0, err
	}
	cmd := exec.CommandContext(ctx, binary, "serve", "--join")
	// Muse writes diagnostics there; nothing reads them, so they go nowhere.
	cmd.Stderr = io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, 0, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, 0, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, 0, err
	}
	stop := func() {
		_ = in.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	return in, out, stop, cmd.Process.Pid, nil
}

// museBinary finds Muse's launcher: on PATH, else where its installer puts it.
func museBinary() (string, error) {
	if binary, err := exec.LookPath("muse"); err == nil {
		return binary, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	binary := filepath.Join(home, ".local", "bin", "muse")
	if info, err := os.Stat(binary); err == nil && !info.IsDir() {
		return binary, nil
	}
	return "", errors.New("source: Muse is not installed")
}

// run keeps a connection to Muse until ctx ends, starting the child again
// whenever it exits. With Muse not installed it looks again now and then.
func (h *museHost) run(ctx context.Context) {
	wait := time.Second
	for ctx.Err() == nil {
		began := time.Now()
		_ = h.connect(ctx)
		if time.Since(began) > time.Minute {
			wait = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, museRetryMax)
	}
}

// connect runs one connection to its end.
func (h *museHost) connect(ctx context.Context) error {
	in, out, stop, pid, err := h.start(ctx)
	if err != nil {
		return err
	}
	h.child.Store(int64(pid))
	defer func() { h.child.Store(0); stop() }()
	conn := &museConn{in: in}
	read := make(chan struct{})
	go func() {
		h.read(conn, out)
		close(read)
	}()

	initCtx, cancel := context.WithTimeout(ctx, museConnectTimeout)
	err = conn.call(initCtx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": museClientName, "title": "Agentman", "version": "1"},
		"capabilities": map[string]any{},
	}, nil)
	cancel()
	if err == nil {
		err = conn.notify("initialized", nil)
	}
	if err != nil {
		return err
	}

	h.mu.Lock()
	h.conn = conn
	close(h.up)
	watched := make([]string, 0, len(h.views))
	for sessionID := range h.views {
		watched = append(watched, sessionID)
	}
	h.mu.Unlock()
	for _, sessionID := range watched {
		_ = conn.call(ctx, "view/subscribe", map[string]any{"sessionId": sessionID}, nil)
	}

	select {
	case <-ctx.Done():
	case <-read:
	}
	h.mu.Lock()
	if h.conn == conn {
		h.conn = nil
		h.up = make(chan struct{})
	}
	h.mu.Unlock()
	return nil
}

// read hands each reply to its caller and each notification to every
// listener, until the child's output ends.
func (h *museHost) read(conn *museConn, out io.Reader) {
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 0, 64<<10), museLineLimit)
	for scanner.Scan() {
		var message museMessage
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			continue
		}
		if message.Method == "" && len(message.ID) > 0 {
			if waiter, ok := conn.pending.LoadAndDelete(string(message.ID)); ok {
				waiter.(chan museMessage) <- message
			}
			continue
		}
		if message.Method == "" {
			continue
		}
		h.mu.Lock()
		listeners := make([]func(museMessage), 0, len(h.listeners))
		for _, listener := range h.listeners {
			listeners = append(listeners, listener)
		}
		h.mu.Unlock()
		for _, listener := range listeners {
			listener(message)
		}
	}
	// Whoever still waits learns the connection is gone.
	conn.pending.Range(func(key, waiter any) bool {
		conn.pending.Delete(key)
		waiter.(chan museMessage) <- museMessage{Error: &museRPCError{Code: -1, Message: "connection closed"}}
		return true
	})
}

// listen calls fn with every notification until the returned func is called.
func (h *museHost) listen(fn func(museMessage)) func() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextKey++
	key := h.nextKey
	h.listeners[key] = fn
	return func() {
		h.mu.Lock()
		delete(h.listeners, key)
		h.mu.Unlock()
	}
}

// isUp reports a live connection, without waiting for one.
func (h *museHost) isUp() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conn != nil
}

// connection waits briefly for Muse to be connected.
func (h *museHost) connection(ctx context.Context) (*museConn, error) {
	h.mu.Lock()
	conn, up := h.conn, h.up
	h.mu.Unlock()
	if conn != nil {
		return conn, nil
	}
	wait, cancel := context.WithTimeout(ctx, museConnectTimeout)
	defer cancel()
	select {
	case <-up:
	case <-wait.Done():
		return nil, errMuseDown
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn == nil {
		return nil, errMuseDown
	}
	return h.conn, nil
}

// call sends a request and decodes its result into dest, which may be nil.
func (h *museHost) call(ctx context.Context, method string, params, dest any) error {
	conn, err := h.connection(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, museCallTimeout)
	defer cancel()
	return conn.call(ctx, method, params, dest)
}

// watch follows a session's view live until the returned func is called;
// watches are counted, so one view serves every watcher.
func (h *museHost) watch(ctx context.Context, sessionID string) func() {
	h.mu.Lock()
	h.views[sessionID]++
	first := h.views[sessionID] == 1
	h.mu.Unlock()
	if first {
		_ = h.call(ctx, "view/subscribe", map[string]any{"sessionId": sessionID}, nil)
	}
	return func() {
		h.mu.Lock()
		h.views[sessionID]--
		last := h.views[sessionID] <= 0
		if last {
			delete(h.views, sessionID)
		}
		h.mu.Unlock()
		if last {
			_ = h.call(context.Background(), "view/unsubscribe", map[string]any{"sessionId": sessionID}, nil)
		}
	}
}

func (c *museConn) call(ctx context.Context, method string, params, dest any) error {
	id := strconv.FormatInt(c.nextID.Add(1), 10)
	waiter := make(chan museMessage, 1)
	c.pending.Store(id, waiter)
	message := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": method}
	if params != nil {
		message["params"] = params
	}
	if err := c.write(message); err != nil {
		c.pending.Delete(id)
		return err
	}
	select {
	case <-ctx.Done():
		c.pending.Delete(id)
		return fmt.Errorf("muse: %s: %w", method, ctx.Err())
	case reply := <-waiter:
		if reply.Error != nil {
			return reply.Error
		}
		if dest != nil && len(reply.Result) > 0 {
			return json.Unmarshal(reply.Result, dest)
		}
		return nil
	}
}

func (c *museConn) notify(method string, params any) error {
	message := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		message["params"] = params
	}
	return c.write(message)
}

func (c *museConn) write(message any) error {
	line, err := json.Marshal(message)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.in.Write(append(line, '\n'))
	return err
}

// museCommandID mints the UUIDv7 every MSP command carries.
func museCommandID() string {
	var b [16]byte
	ms := uint64(time.Now().UnixMilli())
	for i := 0; i < 6; i++ {
		b[i] = byte(ms >> (8 * (5 - i)))
	}
	_, _ = rand.Read(b[6:])
	b[6] = b[6]&0x0F | 0x70
	b[8] = b[8]&0x3F | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
