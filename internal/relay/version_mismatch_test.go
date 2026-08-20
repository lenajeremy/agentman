package relay

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// TestOldClientLearnsWhyItWasDisconnected covers the failure the version gate
// is most likely to cause in the field. A shipped app on the previous protocol
// version reconnects on every close, so a bare close is indistinguishable from
// a flaky network and the app retries forever showing "offline". The relay must
// state the reason, in the version the peer can still decode, before hanging
// up — the frame has to be flushed, because closing discards the send queue.
func TestOldClientLearnsWhyItWasDisconnected(t *testing.T) {
	_, ts := newTestServer(t)
	const daemonToken = "version-mismatch-token"
	dialDaemon(t, ts.URL, daemonToken)
	app := dialPairedApp(t, ts.URL, daemonToken)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	envelope, err := protocol.NewEnvelope("old-1", protocol.PeerDaemon, protocol.Request{
		Type: protocol.ReqListSessions,
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope.V = protocol.Version - 1 // the previously shipped app
	frame, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, data, err := app.Read(ctx)
	if err != nil {
		t.Fatalf("old client was disconnected with no explanation: %v", err)
	}
	var reply protocol.Envelope
	if err := json.Unmarshal(data, &reply); err != nil {
		t.Fatal(err)
	}
	// An old client rejects anything that is not its own version, so the reply
	// is only useful if it arrives in the version that client already speaks.
	if reply.V != protocol.Version-1 {
		t.Fatalf("reply version = %d, want %d — an old client would discard this",
			reply.V, protocol.Version-1)
	}
	if reply.ReplyTo != "old-1" {
		t.Fatalf("reply does not correlate to the request: %q", reply.ReplyTo)
	}
	var control protocol.Control
	if err := json.Unmarshal(reply.Payload, &control); err != nil {
		t.Fatal(err)
	}
	if control.Type != protocol.CtlError || control.Message != "unsupported protocol version" {
		t.Fatalf("unexpected control payload: %+v", control)
	}

	// The socket must still close afterwards: a half-compatible peer that keeps
	// talking is worse than one that is disconnected.
	if _, _, err := app.Read(ctx); err == nil {
		t.Fatal("relay kept an incompatible peer connected")
	}
}
