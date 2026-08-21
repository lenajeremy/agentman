package push

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const goodToken = "ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx]"

func TestValidTokenRejectsAnythingNotFromExpo(t *testing.T) {
	for _, value := range []string{
		"", "hello", "ExponentPushToken[", "ExponentPushToken[abc",
		"https://evil.example.com/ExponentPushToken[abc]",
	} {
		if ValidToken(value) {
			t.Fatalf("accepted %q as a push token", value)
		}
	}
	if !ValidToken(goodToken) {
		t.Fatal("rejected a well-formed token")
	}
}

func TestStoreRoundTripsThroughDisk(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	added, err := store.Register(goodToken)
	if err != nil || !added {
		t.Fatalf("register: added=%v err=%v", added, err)
	}
	// Re-registering is the app reconnecting, not a new device.
	added, err = store.Register(goodToken)
	if err != nil || added {
		t.Fatalf("re-register reported a new device: added=%v err=%v", added, err)
	}

	info, err := os.Stat(filepath.Join(dir, "push.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("token file mode = %v, want 0600", mode)
	}

	reloaded := NewStore(dir)
	if got := reloaded.Tokens(); len(got) != 1 || got[0] != goodToken {
		t.Fatalf("reloaded tokens = %v", got)
	}
}

func TestSendDropsATokenExpoReportsUnregistered(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if _, err := store.Register(goodToken); err != nil {
		t.Fatal(err)
	}

	var received []expoMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			`{"data":[{"status":"error","message":"gone","details":{"error":"DeviceNotRegistered"}}]}`))
	}))
	defer server.Close()

	sender := NewSender(store, Config{})
	sender.Endpoint = server.URL
	if err := sender.Send(context.Background(), Alert{
		Title: "Claude finished", Body: "poll-to-hooks", SessionID: "claude:abc",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(received) != 1 || received[0].To != goodToken {
		t.Fatalf("expo received %+v", received)
	}
	if received[0].Data["sessionId"] != "claude:abc" {
		t.Fatalf("session id not carried for deep linking: %+v", received[0].Data)
	}
	if got := store.Tokens(); len(got) != 0 {
		t.Fatalf("dead token was kept: %v", got)
	}
}

// Transcript text passes through Expo and Apple, so it must not ride along
// unless the user has asked for it.
func TestPreviewIsOptIn(t *testing.T) {
	if got := NewSender(nil, Config{}).Preview("secret output"); got != "" {
		t.Fatalf("preview leaked while disabled: %q", got)
	}
	got := NewSender(nil, Config{IncludePreview: true}).Preview("  secret\n  output  ")
	if got != "secret output" {
		t.Fatalf("preview = %q", got)
	}
}

func TestSendWithNoDevicesDoesNotCallExpo(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	sender := NewSender(NewStore(t.TempDir()), Config{})
	sender.Endpoint = server.URL
	if err := sender.Send(context.Background(), Alert{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("posted to expo with no registered devices")
	}
}
