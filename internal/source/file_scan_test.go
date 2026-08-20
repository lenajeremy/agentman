package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudePageHonorsCancelledContext(t *testing.T) {
	home := fakeClaudeHome(t, "/Users/me/work/proj", "cancel-page", "idle")
	source, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	source.listPanes = noPanes
	if _, err := source.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Page(ctx, "claude:cancel-page", "", 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Claude page returned %v", err)
	}
}

func TestCodexPageHonorsCancelledContext(t *testing.T) {
	home := fakeCodexHome(t, "01900000-aaaa-bbbb-cccc-000000000099", "/Users/me/repo", time.Minute)
	source, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	source.processCheck = alwaysRunning
	source.listPanes = noPanes
	sessions, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("discovered %d Codex sessions, want 1", len(sessions))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Page(ctx, sessions[0].ID, "", 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Codex page returned %v", err)
	}
}

func TestCodexActivityHonorsCancelledContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte("{\"noise\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := codexActivity(ctx, path, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Codex activity scan returned %v", err)
	}
}
