package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// A daemon that connected just as an app did made the app receive
// daemon_online and then a stale hello saying the daemon was offline. The app
// applies them in order, so the phone showed the Mac offline while it was
// connected, with sending disabled, most likely right after a relay restart
// when phones and daemons reconnect together.
func TestAnAppIsNeverToldOfflineAfterOnline(t *testing.T) {
	server, ts := newTestServer(t)
	const daemonToken = "daemon-token-hello"
	account := DeriveAccount(daemonToken)
	deviceToken := deviceTokenFor(t, daemonToken)

	decided, proceed := make(chan struct{}), make(chan struct{})
	server.afterAppHelloDecided = func() { close(decided); <-proceed }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app, _, err := websocket.Dial(ctx, wsAddr(ts.URL)+"/ws/app", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + deviceToken}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.CloseNow()

	<-decided
	dialDaemon(t, server, ts.URL, daemonToken) // registers, and tells the apps
	if online, _ := server.hub.DaemonOnline(account); !online {
		t.Fatal("daemon not registered")
	}
	close(proceed)

	var last protocol.Control
	for range 2 {
		_, data, err := app.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var envelope protocol.Envelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			t.Fatal(err)
		}
		last = protocol.Control{}
		if err := json.Unmarshal(envelope.Payload, &last); err != nil {
			t.Fatal(err)
		}
	}
	if !last.DaemonOnline {
		t.Fatalf("the app's last word was %s offline while the daemon was connected", last.Type)
	}
}
