package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

func TestPhoneLaunchedClaudeVisibleBeforeRegistryExists(t *testing.T) {
	home := t.TempDir()
	source, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	const uuid = "550e8400-e29b-41d4-a716-446655440000"
	pane := tmux.Session{Name: tmux.Prefix + "claude-" + uuid,
		PanePID: 12345, Cwd: "/Users/me/project", Created: time.Now()}
	source.listPanes = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{pane}, nil
	}
	source.snapshotProcesses = nil
	sessions, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "claude:"+uuid ||
		sessions[0].Inject != protocol.InjectTmux {
		t.Fatalf("early Claude session = %+v", sessions)
	}
	page, err := source.Page(context.Background(), sessions[0].ID, "", 10)
	if err != nil || len(page.Messages) != 0 {
		t.Fatalf("page before transcript = %+v, %v", page, err)
	}
}

func TestClaudeDiscoveryTakesOneProcessSnapshotForManyCandidates(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	const sessionCount = 20
	for index := range sessionCount {
		record := map[string]any{
			"pid":       os.Getpid(),
			"sessionId": fmt.Sprintf("session-%d", index),
			"cwd":       fmt.Sprintf("/tmp/project-%d", index),
			"startedAt": time.Now().Add(-time.Minute).UnixMilli(),
			"updatedAt": time.Now().UnixMilli(),
			"status":    "idle",
		}
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(sessionsDir, fmt.Sprintf("session-%d.json", index)), raw, 0o600,
		); err != nil {
			t.Fatal(err)
		}
	}

	source, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	source.listPanes = func(context.Context) ([]tmux.Session, error) {
		panes := make([]tmux.Session, 50)
		for index := range panes {
			panes[index] = tmux.Session{
				Name: fmt.Sprintf("agentman-claude-%d", index), PanePID: 100_000 + index,
			}
		}
		return panes, nil
	}
	snapshots := 0
	source.snapshotProcesses = func(ctx context.Context) (*tmux.ProcessTree, error) {
		snapshots++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return &tmux.ProcessTree{}, nil
	}

	sessions, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != sessionCount {
		t.Fatalf("discovered %d sessions, want %d", len(sessions), sessionCount)
	}
	if snapshots != 1 {
		t.Fatalf("took %d process snapshots for %d pane/session candidates; want 1",
			snapshots, sessionCount*50)
	}
}

func TestClaudeDiscoveryStopsAfterCancelledProcessSnapshot(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "session.json"), []byte(`{
		"pid": 123, "sessionId": "session", "cwd": "/tmp", "status": "idle"
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	source, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	source.listPanes = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{Name: "agentman-claude-test", PanePID: 100}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	source.snapshotProcesses = func(ctx context.Context) (*tmux.ProcessTree, error) {
		cancel()
		return nil, ctx.Err()
	}

	if _, err := source.Discover(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled discovery returned %v", err)
	}
}
