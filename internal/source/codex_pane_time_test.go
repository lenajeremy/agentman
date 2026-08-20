package source

import (
	"context"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

func TestCodexPaneRejectsRolloutThatPredatesItsCreation(t *testing.T) {
	const (
		cwd        = "/Users/me/new-codex-run"
		oldID      = "01900000-aaaa-bbbb-cccc-000000000077"
		newID      = "01900000-aaaa-bbbb-cccc-000000000078"
		paneName   = tmux.Prefix + "codex-new"
		paneAge    = time.Minute
		rolloutAge = 5 * time.Minute
	)
	home := fakeCodexHome(t, oldID, cwd, rolloutAge)
	paneCreated := time.Now().Add(-paneAge)

	source, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	source.processCheck = alwaysRunning
	source.listPanes = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{
			Name: paneName, PanePID: 4242, Cwd: cwd, Command: "codex", Created: paneCreated,
		}}, nil
	}

	sessions, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertCodexPaneBinding(t, sessions, paneName, paneName)
	for _, session := range sessions {
		if session.NativeID == oldID && session.Inject == protocol.InjectTmux {
			t.Fatal("rollout created before the pane was allowed to claim its input route")
		}
	}

	// Once this pane's own rollout appears after its creation, the stable pane
	// session should attach to that transcript on the next discovery sweep.
	addCodexRollout(t, home, newID, cwd, 30*time.Second)
	sessions, err = source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertCodexPaneBinding(t, sessions, paneName, newID)
}

func assertCodexPaneBinding(t *testing.T, sessions []protocol.Session, paneName, nativeID string) {
	t.Helper()
	wantID := string(protocol.KindCodex) + ":" + tmuxID(paneName)
	for _, session := range sessions {
		if session.ID == wantID {
			if session.Inject != protocol.InjectTmux || session.NativeID != nativeID {
				t.Fatalf("pane session = %+v, want nativeID=%q with tmux injection", session, nativeID)
			}
			return
		}
	}
	t.Fatalf("stable pane session %q missing from %+v", wantID, sessions)
}
