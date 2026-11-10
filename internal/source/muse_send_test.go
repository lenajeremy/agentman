package source

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSlashCompactIsMusesCommandNotAMessage(t *testing.T) {
	source, fake := startFakeMuse(t, t.TempDir())
	fake.answers["session/resume"] = func(json.RawMessage) (any, *museRPCError) {
		return map[string]any{"session": map[string]any{"sessionId": "s", "status": "idle"}}, nil
	}
	fake.answers["session/read"] = func(json.RawMessage) (any, *museRPCError) {
		return map[string]any{"session": map[string]any{"sessionId": "s", "status": "running"}}, nil
	}
	fake.answers["turn/start"] = func(json.RawMessage) (any, *museRPCError) {
		return map[string]any{"status": "accepted"}, nil
	}
	status := "accepted"
	fake.answers["session/compact"] = func(json.RawMessage) (any, *museRPCError) {
		return map[string]any{"status": status}, nil
	}
	if _, err := source.Inject(context.Background(), "muse:s", "  /compact\n"); err != nil {
		t.Fatal(err)
	}
	if !fake.called("session/compact") || fake.called("turn/start") {
		t.Fatalf("compact %v, turn %v", fake.called("session/compact"), fake.called("turn/start"))
	}
	status = "noop"
	if _, err := source.Inject(context.Background(), "muse:s", "/compact"); err == nil || !strings.Contains(err.Error(), "nothing to compact") {
		t.Fatalf("got %v", err)
	}
	if _, err := source.Inject(context.Background(), "muse:s", "/compact the login code please"); err != nil {
		t.Fatal(err)
	}
	if !fake.called("turn/start") {
		t.Fatal("words after /compact should still be a message")
	}
}
