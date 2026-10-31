package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lenajeremy/agentman/internal/protocol"
)

func TestTheAppGateNeedsTheTokenLoopbackAndTheAppsOrigin(t *testing.T) {
	gate := AppGate{Token: "secret"}
	cases := []struct {
		name   string
		url    string
		host   string
		origin string
		ok     bool
	}{
		{"the app on macOS and Linux", "/app?token=secret", "127.0.0.1:8788", "wails://wails", true},
		{"the app on Windows", "/app?token=secret", "127.0.0.1:8788", "http://wails.localhost", true},
		{"the screens in development", "/app?token=secret", "localhost:8788", "http://localhost:9245", true},
		{"a program, not a browser", "/app?token=secret", "127.0.0.1:8788", "", true},
		{"no token", "/app", "127.0.0.1:8788", "wails://wails", false},
		{"a wrong token", "/app?token=guess", "127.0.0.1:8788", "wails://wails", false},
		{"another web page", "/app?token=secret", "127.0.0.1:8788", "https://example.com", false},
		{"DNS rebinding", "/app?token=secret", "evil.example:8788", "wails://wails", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, c.url, nil)
		r.Host = c.host
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if err := gate.Allow(r); (err == nil) != c.ok {
			t.Errorf("%s: Allow = %v, want ok=%v", c.name, err, c.ok)
		}
	}
	if (AppGate{}).Allow(httptest.NewRequest(http.MethodGet, "/app?token=", nil)) == nil {
		t.Error("a gate with no token let a request with no token in")
	}
}

func TestTheAppEndpointFileIsForThisUserOnly(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteAppEndpoint(dir, AppEndpoint{URL: "ws://127.0.0.1:8788/app", Token: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteAppEndpoint(dir, AppEndpoint{URL: "ws://127.0.0.1:8788/app", Token: "t2"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
	var got AppEndpoint
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &got); err != nil || got.Token != "t2" {
		t.Fatalf("file = %s (%v), want the second token", data, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".app-*")); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

// An app over the WebSocket is an app like one on the socket: its requests
// are answered, tied to their ids, and it hears every broadcast. Lines may
// arrive split across messages, so the test reads the messages as a stream.
func TestTheAppProtocolOverWebSocket(t *testing.T) {
	h := startLocal(t, func(_ context.Context, device string, req protocol.Request) protocol.Event {
		if req.Type == protocol.ReqListSessions {
			return protocol.Event{Type: protocol.EvtSessions, Sessions: []protocol.Session{{ID: "claude:s1", Name: device}}}
		}
		return protocol.Event{}
	})
	waitForServe(t, h.server)
	web := httptest.NewServer(http.HandlerFunc(h.server.ServeWebSocket))
	defer web.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(web.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	c.SetReadLimit(maxLocalFrame)
	frames := &wsLines{c: c}

	envelope, _ := protocol.NewEnvelope("web-1", protocol.PeerDaemon, protocol.Request{Type: protocol.ReqListSessions})
	line, _ := json.Marshal(envelope)
	if err := c.Write(ctx, websocket.MessageText, append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	reply, event := frames.next(t, ctx)
	if reply.ReplyTo != "web-1" || event.Type != protocol.EvtSessions || len(event.Sessions) != 1 {
		t.Fatalf("reply = %+v / %+v", reply, event)
	}
	if !strings.HasPrefix(event.Sessions[0].Name, "local:") {
		t.Fatalf("the request came from %q, want a local app id", event.Sessions[0].Name)
	}

	_ = h.server.Send(protocol.Event{Type: protocol.EvtSessionGone, SessionID: "claude:s1"})
	broadcast, event := frames.next(t, ctx)
	if broadcast.ReplyTo != "" || event.Type != protocol.EvtSessionGone {
		t.Fatalf("broadcast = %+v / %+v", broadcast, event)
	}
}

func TestTheWebSocketWaitsForTheDaemon(t *testing.T) {
	server := NewLocalServer(socketPath(t))
	rec := httptest.NewRecorder()
	server.ServeWebSocket(rec, httptest.NewRequest(http.MethodGet, "/app", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d before Serve, want 503", rec.Code)
	}
}

func waitForServe(t *testing.T, s *LocalServer) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		ready := s.handler != nil
		s.mu.Unlock()
		if ready {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Serve never started")
}

// wsLines reads newline-delimited envelopes out of a stream of messages.
type wsLines struct {
	c   *websocket.Conn
	buf []byte
}

func (w *wsLines) next(t *testing.T, ctx context.Context) (protocol.Envelope, protocol.Event) {
	t.Helper()
	for {
		if i := bytes.IndexByte(w.buf, '\n'); i >= 0 {
			line := w.buf[:i]
			w.buf = w.buf[i+1:]
			var envelope protocol.Envelope
			if err := json.Unmarshal(line, &envelope); err != nil {
				t.Fatalf("not an envelope: %v\n%s", err, line)
			}
			var event protocol.Event
			if err := json.Unmarshal(envelope.Payload, &event); err != nil {
				t.Fatalf("not an event: %v", err)
			}
			return envelope, event
		}
		_, data, err := w.c.Read(ctx)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		w.buf = append(w.buf, data...)
	}
}
