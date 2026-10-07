package push

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
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
	added, err := store.Register(goodToken, nil)
	if err != nil || !added {
		t.Fatalf("register: added=%v err=%v", added, err)
	}
	// Re-registering is the app reconnecting, not a new device.
	added, err = store.Register(goodToken, nil)
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
	if _, err := store.Register(goodToken, nil); err != nil {
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

// A phone chooses which alerts it wants, and the choice survives a restart.
func TestEachPhoneGetsOnlyTheAlertsItChose(t *testing.T) {
	const quiet = "ExponentPushToken[qqqqqqqqqqqqqqqqqqqqqq]"
	const everything = "ExponentPushToken[eeeeeeeeeeeeeeeeeeeeee]"
	dir := t.TempDir()
	store := NewStore(dir)
	if _, err := store.Register(quiet, &Prefs{Finished: false, NeedsYou: true}); err != nil {
		t.Fatal(err)
	}
	// An app from before the choice existed sends none, and has always been
	// sent everything.
	if _, err := store.Register(everything, nil); err != nil {
		t.Fatal(err)
	}

	check := func(label string, s *Store) {
		t.Helper()
		if got := sorted(s.TokensFor(KindFinished)); len(got) != 1 || got[0] != everything {
			t.Errorf("%s: finished goes to %v, want only the phone that kept it", label, got)
		}
		if got := sorted(s.TokensFor(KindNeedsYou)); len(got) != 2 {
			t.Errorf("%s: needs-you goes to %v, want both phones", label, got)
		}
	}
	check("live", store)
	check("after a restart", NewStore(dir))

	// Re-registering without a choice, as every reconnect of an old app does,
	// keeps the choice already made rather than resetting it.
	if _, err := store.Register(quiet, nil); err != nil {
		t.Fatal(err)
	}
	check("after a plain re-registration", store)

	// And a new choice replaces the old one.
	if _, err := store.Register(quiet, &Prefs{Finished: true, NeedsYou: false}); err != nil {
		t.Fatal(err)
	}
	if got := sorted(store.TokensFor(KindNeedsYou)); len(got) != 1 || got[0] != everything {
		t.Errorf("needs-you goes to %v after the phone turned it off", got)
	}
}

// A token file written before phones could choose reads as everything on.
func TestATokenFileFromBeforeTheChoiceKeepsEveryAlert(t *testing.T) {
	dir := t.TempDir()
	stored := []map[string]any{{"value": goodToken, "lastSeen": time.Now().UnixMilli()}}
	raw, _ := json.Marshal(stored)
	if err := os.WriteFile(filepath.Join(dir, "push.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(dir)
	if len(store.TokensFor(KindFinished)) != 1 || len(store.TokensFor(KindNeedsYou)) != 1 {
		t.Fatal("an old token file lost alerts it used to get")
	}
}

// Send asks Expo only for the phones that want the alert, and calls nothing
// when none does.
func TestSendSkipsPhonesThatTurnedTheKindOff(t *testing.T) {
	const quiet = "ExponentPushToken[qqqqqqqqqqqqqqqqqqqqqq]"
	store := NewStore(t.TempDir())
	if _, err := store.Register(quiet, &Prefs{Finished: false, NeedsYou: true}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var received []expoMessage
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"status":"ok"}]}`))
	}))
	defer server.Close()
	sender := NewSender(store, Config{})
	sender.Endpoint = server.URL

	if err := sender.Send(context.Background(), Alert{Kind: KindFinished, Title: "done"}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("Expo was called %d times for an alert nobody wants", calls)
	}
	if err := sender.Send(context.Background(), Alert{Kind: KindNeedsYou, Title: "needs you"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("Expo was called %d times for an alert the phone wants", calls)
	}
}

func TestEachKindPlaysItsOwnSound(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Register(goodToken, nil); err != nil {
		t.Fatal(err)
	}
	var sounds []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var received []expoMessage
		_ = json.NewDecoder(r.Body).Decode(&received)
		for _, message := range received {
			sounds = append(sounds, message.Sound)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"status":"ok"}]}`))
	}))
	defer server.Close()
	sender := NewSender(store, Config{})
	sender.Endpoint = server.URL

	for _, kind := range []Kind{KindNeedsYou, KindFinished, ""} {
		if err := sender.Send(context.Background(), Alert{Kind: kind, Title: "t"}); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{SoundNeedsYou, SoundFinished, "default"}
	if len(sounds) != len(want) {
		t.Fatalf("sounds = %v, want %v", sounds, want)
	}
	for i := range want {
		if sounds[i] != want[i] {
			t.Fatalf("sounds = %v, want %v", sounds, want)
		}
	}
}

func sorted(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
