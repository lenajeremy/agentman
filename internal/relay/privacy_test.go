package relay

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The account is a hash of the daemon's token: stable for as long as the
// token is, so logging it on every connect and disconnect wrote a durable
// per-user presence timeline into the host's log retention, for a relay that
// says it stores nothing. A connection is logged by an id of its own.
func TestDaemonConnectionsAreLoggedWithoutTheirAccount(t *testing.T) {
	logs := &lockedBuffer{}
	server := NewServer(testSecret, "test", slog.New(slog.NewTextHandler(logs, nil)), true)
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)

	const token = "daemon-token-private"
	daemon := dialDaemon(t, server, ts.URL, token)
	daemon.CloseNow()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "daemon disconnected") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	text := logs.String()
	if !strings.Contains(text, "daemon connected") || !strings.Contains(text, "daemon disconnected") {
		t.Fatalf("connection events were not logged:\n%s", text)
	}
	if account := string(DeriveAccount(token)); strings.Contains(text, account) || strings.Contains(text, account[:12]) {
		t.Errorf("the log names the account:\n%s", text)
	}
}

// /health is public and unauthenticated. It said how many daemons, apps and
// pairings the relay held, which is enough to watch the public relay's user
// base, and counting pairings walked the whole map under the hub's lock on
// every request. Status and version are all a health check needs.
func TestHealthSaysOnlyThatTheRelayIsUp(t *testing.T) {
	server, ts := newTestServer(t)
	dialDaemon(t, server, ts.URL, "daemon-token")
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"daemons", "apps", "pendingPairings"} {
		if _, ok := payload[field]; ok {
			t.Errorf("/health still reports %q", field)
		}
	}
	if payload["status"] != "ok" || payload["version"] != "test" {
		t.Errorf("/health = %v", payload)
	}
}
