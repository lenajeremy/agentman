package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// socketPath is short on purpose: a test's own temp dir on macOS can push a
// socket path past the 103 bytes sockaddr_un allows.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "am")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "am.sock")
}

type localHarness struct {
	server *LocalServer
	cancel context.CancelFunc
	done   chan error

	mu   sync.Mutex
	gone []string
}

func startLocal(t *testing.T, handler func(context.Context, string, protocol.Request) protocol.Event) *localHarness {
	t.Helper()
	h := &localHarness{server: NewLocalServer(socketPath(t)), done: make(chan error, 1)}
	if err := h.server.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() {
		h.done <- h.server.Serve(ctx, handler, func(id string) {
			h.mu.Lock()
			h.gone = append(h.gone, id)
			h.mu.Unlock()
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-h.done:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after the daemon stopped")
		}
	})
	return h
}

func (h *localHarness) disconnected() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.gone...)
}

type localApp struct {
	conn   net.Conn
	reader *bufio.Reader
}

func dialLocal(t *testing.T, path string) *localApp {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &localApp{conn: conn, reader: bufio.NewReader(conn)}
}

func (a *localApp) send(t *testing.T, id string, req protocol.Request) {
	t.Helper()
	envelope, err := protocol.NewEnvelope(id, protocol.PeerDaemon, req)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(envelope)
	if _, err := a.conn.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
}

func (a *localApp) read(t *testing.T) (protocol.Envelope, protocol.Event) {
	t.Helper()
	_ = a.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := a.reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("reading a frame: %v", err)
	}
	var envelope protocol.Envelope
	if err := json.Unmarshal(line, &envelope); err != nil {
		t.Fatalf("frame is not an envelope: %v\n%s", err, line)
	}
	var event protocol.Event
	if err := json.Unmarshal(envelope.Payload, &event); err != nil {
		t.Fatalf("payload is not an event: %v", err)
	}
	return envelope, event
}

// An app on this Mac asks over the socket and gets its answer back, tied to
// its request, and then hears what the daemon tells every app.
func TestTheLocalSocketAnswersAndBroadcasts(t *testing.T) {
	var from string
	h := startLocal(t, func(_ context.Context, device string, req protocol.Request) protocol.Event {
		from = device
		if req.Type == protocol.ReqListSessions {
			return protocol.Event{Type: protocol.EvtSessions, Sessions: []protocol.Session{{ID: "claude:s1"}}}
		}
		return protocol.Event{}
	})
	app := dialLocal(t, h.server.Path())

	app.send(t, "req-1", protocol.Request{Type: protocol.ReqListSessions})
	envelope, event := app.read(t)
	if envelope.ReplyTo != "req-1" || event.Type != protocol.EvtSessions || len(event.Sessions) != 1 {
		t.Fatalf("reply = %+v / %+v", envelope, event)
	}
	if !strings.HasPrefix(from, "local:") {
		t.Fatalf("the request was attributed to %q, want this connection's local id", from)
	}

	_ = h.server.Send(protocol.Event{Type: protocol.EvtSessionGone, SessionID: "claude:s1"})
	envelope, event = app.read(t)
	if envelope.ReplyTo != "" || event.Type != protocol.EvtSessionGone {
		t.Fatalf("broadcast = %+v / %+v", envelope, event)
	}
}

func TestTheLocalSocketIsForThisUserOnly(t *testing.T) {
	h := startLocal(t, func(context.Context, string, protocol.Request) protocol.Event { return protocol.Event{} })
	info, err := os.Stat(h.server.Path())
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("socket mode = %o, want 600", mode)
	}
}

// A second daemon must not take the socket from a running one; a socket file
// left by one that has exited is simply replaced.
func TestASecondDaemonDoesNotTakeTheSocket(t *testing.T) {
	h := startLocal(t, func(context.Context, string, protocol.Request) protocol.Event { return protocol.Event{} })
	if err := NewLocalServer(h.server.Path()).Listen(); !errors.Is(err, ErrLocalSocketInUse) {
		t.Fatalf("second Listen = %v, want ErrLocalSocketInUse", err)
	}

	stale := socketPath(t)
	listener, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	// Closing a Go listener unlinks its file; recreate it as the leftover a
	// crashed daemon leaves behind.
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	listener.Close()
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("no stale socket to test with: %v", err)
	}
	fresh := NewLocalServer(stale)
	if err := fresh.Listen(); err != nil {
		t.Fatalf("a stale socket was not replaced: %v", err)
	}
	fresh.listener.Close()
}

// An app that leaves takes its subscriptions with it, as a phone does.
func TestAnAppThatLeavesIsReported(t *testing.T) {
	h := startLocal(t, func(context.Context, string, protocol.Request) protocol.Event { return protocol.Event{} })
	app := dialLocal(t, h.server.Path())
	app.send(t, "sub", protocol.Request{Type: protocol.ReqSubscribe, SessionID: "claude:s1"})
	time.Sleep(50 * time.Millisecond)
	app.conn.Close()

	deadline := time.Now().Add(5 * time.Second)
	for len(h.disconnected()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if gone := h.disconnected(); len(gone) != 1 || !strings.HasPrefix(gone[0], "local:") {
		t.Fatalf("disconnected = %v, want the app's local id once", gone)
	}
}

func TestAnAppOnAnotherProtocolVersionIsTold(t *testing.T) {
	h := startLocal(t, func(context.Context, string, protocol.Request) protocol.Event { return protocol.Event{} })
	app := dialLocal(t, h.server.Path())
	line := `{"v":999,"id":"old","to":"daemon","payload":{"type":"list_sessions"}}` + "\n"
	if _, err := app.conn.Write([]byte(line)); err != nil {
		t.Fatal(err)
	}
	envelope, event := app.read(t)
	if envelope.ReplyTo != "old" || event.Type != protocol.EvtError || !strings.Contains(event.Error, "version") {
		t.Fatalf("reply = %+v / %+v", envelope, event)
	}
}

// An app that stops reading is cut off, and the daemon's broadcasts never
// wait for it.
func TestAnAppThatStopsReadingIsDropped(t *testing.T) {
	h := startLocal(t, func(context.Context, string, protocol.Request) protocol.Event { return protocol.Event{} })
	app := dialLocal(t, h.server.Path())
	app.send(t, "hello", protocol.Request{Type: protocol.ReqListSessions})

	deadline := time.Now().Add(5 * time.Second)
	for {
		h.server.mu.Lock()
		connected := len(h.server.conns)
		h.server.mu.Unlock()
		if connected == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the app never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}

	big := strings.Repeat("x", 64<<10)
	started := time.Now()
	for i := 0; i < localOutbox*4; i++ {
		_ = h.server.Send(protocol.Event{Type: protocol.EvtTurnComplete, Preview: big})
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("broadcasting to an app that does not read took %v", elapsed)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		h.server.mu.Lock()
		connected := len(h.server.conns)
		h.server.mu.Unlock()
		if connected == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("an app that stopped reading was never dropped")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
