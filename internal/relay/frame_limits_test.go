package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// dialPairedApp connects an app to the same account as daemonToken.
func dialPairedApp(t *testing.T, base, daemonToken string) *websocket.Conn {
	t.Helper()
	code := requestPairCode(t, base, daemonToken)
	deviceToken, status := redeem(t, base, code)
	if status != http.StatusOK || deviceToken == "" {
		t.Fatalf("redeem failed: HTTP %d", status)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app, _, err := websocket.Dial(ctx, wsAddr(base)+"/ws/app", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + deviceToken}},
	})
	if err != nil {
		t.Fatalf("app dial: %v", err)
	}
	t.Cleanup(func() { app.CloseNow() })
	app.SetReadLimit(maxFrame)
	// Drain the hello the relay sends on connect.
	if _, _, err := app.Read(ctx); err != nil {
		t.Fatalf("app hello: %v", err)
	}
	return app
}

// TestDaemonMayPublishLargeEvent pins the asymmetry the frame limits exist to
// express. A daemon publishes history pages and session snapshots that are
// legitimately far larger than any app request: it caps its own events at
// maxEventBytes (3 MiB) and allows a single message of maxWireMessageBytes
// (256 KiB), so one long tool result routinely exceeds an app-sized ceiling.
// Holding the daemon to maxAppFrame silently killed its socket mid-page.
func TestDaemonMayPublishLargeEvent(t *testing.T) {
	_, ts := newTestServer(t)
	const daemonToken = "large-event-token"
	daemon := dialDaemon(t, ts.URL, daemonToken)
	app := dialPairedApp(t, ts.URL, daemonToken)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	body := strings.Repeat("x", 512<<10) // 512 KiB: over maxAppFrame, under maxFrame
	envelope, err := protocol.NewEnvelope("probe-1", protocol.PeerApp, protocol.Event{
		Type: protocol.EvtMessages, SessionID: "claude:big", Error: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if len(frame) <= maxAppFrame || len(frame) >= maxFrame {
		t.Fatalf("probe frame is %d bytes, which does not sit between %d and %d",
			len(frame), maxAppFrame, maxFrame)
	}

	if err := daemon.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatalf("relay refused a %d byte daemon event: %v", len(frame), err)
	}

	_, data, err := app.Read(ctx)
	if err != nil {
		t.Fatalf("app never received the large event: %v", err)
	}
	var received protocol.Envelope
	if err := json.Unmarshal(data, &received); err != nil {
		t.Fatal(err)
	}
	var event protocol.Event
	if err := json.Unmarshal(received.Payload, &event); err != nil {
		t.Fatal(err)
	}
	if len(event.Error) != len(body) {
		t.Fatalf("payload arrived truncated: %d bytes, want %d", len(event.Error), len(body))
	}
}

// TestAppFrameIsCappedAtSocket is the other half: an app only ever sends
// requests, so an oversized app frame must be rejected by the socket itself
// rather than allocated first and length-checked afterwards.
func TestAppFrameIsCappedAtSocket(t *testing.T) {
	_, ts := newTestServer(t)
	const daemonToken = "app-cap-token"
	dialDaemon(t, ts.URL, daemonToken)
	app := dialPairedApp(t, ts.URL, daemonToken)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	envelope, err := protocol.NewEnvelope("probe-2", protocol.PeerDaemon, protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:big",
		Text: strings.Repeat("y", maxAppFrame),
	})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if len(frame) <= maxAppFrame {
		t.Fatalf("probe frame is only %d bytes, want more than %d", len(frame), maxAppFrame)
	}

	// The write may succeed locally before the relay's close is observed; the
	// read is what proves the relay refused the frame.
	_ = app.Write(ctx, websocket.MessageText, frame)
	if _, _, err := app.Read(ctx); err == nil {
		t.Fatal("relay accepted an oversized app frame")
	}
}
