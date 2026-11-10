package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lenajeremy/agentman/internal/procinfo"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// A conversation is open while a codex process holds its rollout, and not
// once only other processes do, or none.
func TestARolloutIsOpenWhileACodexProcessHoldsIt(t *testing.T) {
	dir := t.TempDir()
	rollout := filepath.Join(dir, "rollout-1.jsonl")
	if err := os.WriteFile(rollout, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(rollout)
	processes := []procinfo.Process{{PID: 1, Comm: "zsh"}, {PID: 2, Comm: "codex"}, {PID: 3, Comm: "codex", Zombie: true}}
	held := map[int][]procinfo.OpenFile{1: {{Path: real}}, 2: {{Path: filepath.Join(dir, "other.jsonl")}}}
	list := func() ([]procinfo.Process, error) { return processes, nil }
	files := func(pid int) ([]procinfo.OpenFile, error) { return held[pid], nil }

	if open, known := heldOpen(rollout, "codex", list, files); open || !known {
		t.Fatalf("held only by a shell: open=%v known=%v", open, known)
	}
	held[2] = append(held[2], procinfo.OpenFile{Path: real})
	if open, _ := heldOpen(rollout, "codex", list, files); !open {
		t.Fatal("held by codex, but not open")
	}
	broken := func() ([]procinfo.Process, error) { return nil, errors.New("unsupported") }
	if _, known := heldOpen(rollout, "codex", broken, files); known {
		t.Fatal("couldn't read processes, but said it knew")
	}
}

// openerSource says whether its one session's conversation is open.
type openerSource struct {
	plainSource
	session protocol.Session
	open    bool
	known   bool
}

func (s *openerSource) Discover(context.Context) ([]protocol.Session, error) {
	return []protocol.Session{s.session}, nil
}

func (s *openerSource) ConversationOpen(protocol.Session) (bool, bool) { return s.open, s.known }

// The source's word beats the pid both ways: open with no pid is running,
// closed with a live pid is not; one that can't tell leaves it to the pid.
func TestASourcesWordOnAnOpenConversationBeatsThePid(t *testing.T) {
	alive := os.Getpid()
	for _, c := range []struct {
		name         string
		pid          int
		open, known  bool
		wantsRunning bool
	}{
		{"open, no pid", 0, true, true, true},
		{"closed, live pid", alive, false, true, false},
		{"can't tell, live pid", alive, false, false, true},
		{"can't tell, no pid", 0, false, false, false},
	} {
		session := protocol.Session{ID: "codex:t1", Kind: protocol.KindCodex, NativeID: "t1",
			State: protocol.StateIdle, Inject: protocol.InjectNone, AgentPID: c.pid}
		registry := NewRegistry()
		registry.Add(&openerSource{plainSource: plainSource{kind: protocol.KindCodex}, session: session, open: c.open, known: c.known})
		if _, err := registry.Discover(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := registry.RunningUnreachable("codex:t1"); got != c.wantsRunning {
			t.Errorf("%s: running = %v, want %v", c.name, got, c.wantsRunning)
		}
	}
}
