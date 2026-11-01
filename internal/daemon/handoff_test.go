package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// fakeHandoff stands in for an adapter's side of a move.
type fakeHandoff struct {
	calls                 []string
	releaseErr, resumeErr error
	releasedTo            string
}

func (f *fakeHandoff) ReleaseDirect(_ context.Context, id string) (string, error) {
	f.calls = append(f.calls, "release direct "+id)
	return f.releasedTo, f.releaseErr
}

func (f *fakeHandoff) ReleasePane(_ context.Context, id string) (string, error) {
	f.calls = append(f.calls, "release pane "+id)
	return f.releasedTo, f.releaseErr
}

func (f *fakeHandoff) ResumeDirect(_ context.Context, id, native, cwd string) (string, error) {
	f.calls = append(f.calls, "resume direct "+id+" as "+native+" in "+cwd)
	return "kiro:" + native, f.resumeErr
}

// Open in terminal and Take over close one side, then open the other on the
// same chat; a side that fails to open puts the session back where it was.
func TestAKiroSessionMovesBetweenACPAndAPane(t *testing.T) {
	if !tmux.Available() {
		t.Skip("reopening in a pane checks for tmux first")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "kiro-cli"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	const chat = "6b1c3a52-7d1e-4c0a-9f3b-2e8d5a4c1f90"
	dir := t.TempDir()
	var opened []string
	paneErr := error(nil)
	previousPane := startResumedPane
	startResumedPane = func(_ context.Context, name, cwd string, argv []string, _ string) error {
		opened = append(opened, name+" in "+cwd+": "+strings.Join(argv[1:], " "))
		return paneErr
	}
	t.Cleanup(func() { startResumedPane = previousPane })
	fake := &fakeHandoff{}
	previous := handoffFor
	handoffFor = func(_ *source.Registry, kind protocol.Kind) (source.Handoff, bool) {
		return fake, kind == protocol.KindKiro
	}
	t.Cleanup(func() { handoffFor = previous })

	move := func(id, handoff string) (string, error) {
		kiro, _ := source.NewKiroSource(t.TempDir())
		d, _ := agentWithSource(t, kiro)
		d.sessions[id] = protocol.Session{ID: id, Kind: protocol.KindKiro, NativeID: chat,
			Cwd: dir, State: protocol.StateIdle, Handoff: handoff}
		return d.handoffSession(context.Background(), id)
	}
	reset := func(releaseErr, resumeErr, pane error) {
		*fake = fakeHandoff{releasedTo: dir, releaseErr: releaseErr, resumeErr: resumeErr}
		opened, paneErr = nil, pane
	}
	const own = "kiro:" + chat
	paneFor := func(opened []string) bool {
		return len(opened) == 1 && strings.HasPrefix(opened[0], "agentman-kiro-chat-"+chat+"-") &&
			strings.HasSuffix(opened[0], " in "+dir+": chat --resume-id "+chat)
	}

	t.Run("open in terminal", func(t *testing.T) {
		reset(nil, nil, nil)
		id, err := move(own, protocol.HandoffTerminal)
		if err != nil || id != own {
			t.Fatalf("moved to %q: %v", id, err)
		}
		if strings.Join(fake.calls, "|") != "release direct "+own || !paneFor(opened) {
			t.Fatalf("calls %q, panes %q", fake.calls, opened)
		}
	})
	t.Run("a pane that will not open puts it back into Agentman", func(t *testing.T) {
		reset(nil, nil, errors.New("no pane"))
		if _, err := move(own, protocol.HandoffTerminal); err == nil || !strings.Contains(err.Error(), "Kiro did not open in a terminal") {
			t.Fatalf("err = %v", err)
		}
		if want := "release direct " + own + "|resume direct " + own + " as " + chat + " in " + dir; strings.Join(fake.calls, "|") != want {
			t.Fatalf("calls %q", fake.calls)
		}
	})
	t.Run("take over a pane that went by its own name", func(t *testing.T) {
		reset(nil, nil, nil)
		const pane = "kiro:tmux-agentman-kiro-1790528359000-ab12"
		id, err := move(pane, protocol.HandoffAgentman)
		if err != nil || id != own || len(opened) != 0 {
			t.Fatalf("moved to %q: %v, panes %q", id, err, opened)
		}
		if want := "release pane " + pane + "|resume direct " + pane + " as " + chat + " in " + dir; strings.Join(fake.calls, "|") != want {
			t.Fatalf("calls %q", fake.calls)
		}
	})
	t.Run("an ACP that will not carry it on puts it back in a pane", func(t *testing.T) {
		reset(nil, errors.New("no kiro"), nil)
		if _, err := move(own, protocol.HandoffAgentman); err == nil || !strings.Contains(err.Error(), "back in its terminal") {
			t.Fatalf("err = %v", err)
		}
		if !paneFor(opened) {
			t.Fatalf("panes %q", opened)
		}
	})
	t.Run("mid-turn, nothing moves", func(t *testing.T) {
		reset(source.ErrKiroBusy, nil, nil)
		if _, err := move(own, protocol.HandoffTerminal); !errors.Is(err, source.ErrKiroBusy) || len(opened) != 0 {
			t.Fatalf("err = %v, panes %q", err, opened)
		}
	})
	t.Run("a session that cannot move", func(t *testing.T) {
		reset(nil, nil, nil)
		if _, err := move(own, ""); err == nil || len(fake.calls) != 0 {
			t.Fatalf("err = %v, calls %q", err, fake.calls)
		}
	})
	t.Run("a session that is not running", func(t *testing.T) {
		reset(nil, nil, nil)
		kiro, _ := source.NewKiroSource(t.TempDir())
		d, _ := agentWithSource(t, kiro)
		if _, err := d.handoffSession(context.Background(), own); err == nil || len(fake.calls) != 0 {
			t.Fatalf("err = %v, calls %q", err, fake.calls)
		}
	})
}
