package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// kiroHandoffSource is a Kiro with the fake ACP server and the panes it is
// told about, where a Kiro holding a chat's lock runs as this test, under a
// pane whose process is 70000.
type kiroHandoffSource struct {
	*KiroSource
	fake   *fakeKiro
	home   string
	mu     sync.Mutex
	panes  []tmux.Session
	screen string
	killed []string
}

func newKiroHandoffSource(t *testing.T) *kiroHandoffSource {
	t.Helper()
	home := t.TempDir()
	table := fmt.Sprintf("%d 70000 kiro-cli-chat\n70000 1 kiro-cli\n", os.Getpid())
	h := &kiroHandoffSource{KiroSource: newTestKiro(t, home, table), home: home,
		screen: "›  ask a question or describe a task ↵\n"}
	h.fake = &fakeKiro{permission: make(chan json.RawMessage, 1), cancel: make(chan struct{}, 1)}
	h.acpDial = h.fake.dial
	h.listPanes = func(context.Context) ([]tmux.Session, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		return append([]tmux.Session(nil), h.panes...), nil
	}
	h.capturePane = func(context.Context, string) (string, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.screen, nil
	}
	// Closing a pane takes its Kiro, and Kiro's lock, with it.
	h.killPane = func(_ context.Context, name string) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.killed = append(h.killed, name)
		h.panes = nil
		return os.Remove(h.lock())
	}
	previous := kiroPanesAvailable
	kiroPanesAvailable = func() bool { return true }
	t.Cleanup(func() {
		kiroPanesAvailable = previous
		h.acp.mu.Lock()
		owned := make([]*kiroACPSession, 0)
		for _, st := range h.acp.sessions {
			owned = append(owned, st)
		}
		h.acp.mu.Unlock()
		for _, st := range owned {
			h.dropACP(st)
		}
	})
	return h
}

func (h *kiroHandoffSource) lock() string {
	return filepath.Join(h.home, ".kiro", "sessions", "cli", fakeKiroSession+".lock")
}

// open has Kiro open the chat in a pane of that name: its lock, held by the
// pane's Kiro.
func (h *kiroHandoffSource) open(t *testing.T, pane, cwd string) {
	t.Helper()
	writeKiroSession(t, h.home, fakeKiroSession, cwd, os.Getpid(), kiroLinePrompt, kiroLineReply)
	h.mu.Lock()
	h.panes = []tmux.Session{{Name: pane, PanePID: 70_000, Cwd: cwd}}
	h.mu.Unlock()
}

func (h *kiroHandoffSource) list(t *testing.T) map[string]protocol.Session {
	t.Helper()
	return discoverKiro(t, h.KiroSource)
}

// Open in terminal: the ACP child is closed between turns, and the chat is
// listed as it was, working, until it is open in its pane under the same id.
func TestKiroOpenInTerminalReleasesTheChat(t *testing.T) {
	h := newKiroHandoffSource(t)
	cwd := t.TempDir()
	id, err := h.StartACPSession(context.Background(), cwd, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if id != "kiro:"+fakeKiroSession {
		t.Fatalf("started %q", id)
	}
	session := awaitKiro(t, h.KiroSource, id, "idle", func(s protocol.Session) bool { return s.State == protocol.StateIdle })
	if session.Handoff != protocol.HandoffTerminal {
		t.Fatalf("handoff = %q, want terminal", session.Handoff)
	}
	released, err := h.ReleaseDirect(context.Background(), id)
	if err != nil || released != cwd {
		t.Fatalf("released to %q: %v", released, err)
	}
	if _, owned := h.acpOwned(id); owned {
		t.Fatal("the ACP child is still Agentman's")
	}
	if held := h.list(t)[id]; held.State != protocol.StateBusy || held.Handoff != "" || held.Inject != protocol.InjectAPI {
		t.Fatalf("between the two sides = %+v", held)
	}
	pane, named := h.ResumedSession(fakeKiroSession, tmux.NewName("kiro"))
	if named != id {
		t.Fatalf("the pane is named %q", named)
	}
	h.open(t, pane, cwd)
	sessions := h.list(t)
	arrived := sessions[id]
	if len(sessions) != 1 || arrived.Inject != protocol.InjectTmux || arrived.State != protocol.StateIdle ||
		arrived.Handoff != protocol.HandoffAgentman {
		t.Fatalf("in its pane = %+v (listed %v)", arrived, sessions)
	}
	if len(h.moves) != 0 {
		t.Fatalf("still moving: %v", h.moves)
	}
}

// Mid-turn nothing moves; and a chat Kiro will not let go of is not opened
// over it.
func TestKiroOpenInTerminalRefusals(t *testing.T) {
	t.Run("mid-turn", func(t *testing.T) {
		h := newKiroHandoffSource(t)
		id, err := h.StartACPSession(context.Background(), t.TempDir(), "hang")
		if err != nil {
			t.Fatal(err)
		}
		awaitKiro(t, h.KiroSource, id, "busy", func(s protocol.Session) bool { return s.State == protocol.StateBusy })
		if _, err := h.ReleaseDirect(context.Background(), id); !errors.Is(err, ErrKiroBusy) {
			t.Fatalf("err = %v", err)
		}
		if _, owned := h.acpOwned(id); !owned {
			t.Fatal("closed mid-turn")
		}
		if err := h.Interrupt(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("held by another process", func(t *testing.T) {
		h := newKiroHandoffSource(t)
		previous := kiroHandoffWait
		kiroHandoffWait = 200 * time.Millisecond
		t.Cleanup(func() { kiroHandoffWait = previous })
		cwd := t.TempDir()
		id, err := h.StartACPSession(context.Background(), cwd, "hello")
		if err != nil {
			t.Fatal(err)
		}
		awaitKiro(t, h.KiroSource, id, "idle", func(s protocol.Session) bool { return s.State == protocol.StateIdle })
		writeKiroSession(t, h.home, fakeKiroSession, cwd, os.Getpid(), kiroLinePrompt)
		if _, err := h.ReleaseDirect(context.Background(), id); err == nil || !strings.Contains(err.Error(), "close it there first") {
			t.Fatalf("err = %v", err)
		}
		if len(h.moves) != 0 {
			t.Fatalf("still moving: %v", h.moves)
		}
	})
}

// Take over: the pane is closed between turns, and the chat carries on over
// ACP under its own id; the pane's own id is the one it took over from.
func TestKiroTakeOverClosesThePane(t *testing.T) {
	h := newKiroHandoffSource(t)
	cwd := t.TempDir()
	const pane = "agentman-kiro-1790528359000-ab12"
	paneID := "kiro:tmux-" + pane
	h.open(t, pane, cwd)
	if session := h.list(t)[paneID]; session.Handoff != protocol.HandoffAgentman || session.NativeID != fakeKiroSession {
		t.Fatalf("in its pane = %+v", session)
	}
	released, err := h.ReleasePane(context.Background(), paneID)
	if err != nil || released != cwd || strings.Join(h.killed, " ") != pane {
		t.Fatalf("released to %q: %v, closed %q", released, err, h.killed)
	}
	if held := h.list(t)[paneID]; held.State != protocol.StateBusy || held.Handoff != "" {
		t.Fatalf("between the two sides = %+v", held)
	}
	id, err := h.ResumeDirect(context.Background(), paneID, fakeKiroSession, cwd)
	if err != nil || id != "kiro:"+fakeKiroSession {
		t.Fatalf("carried on as %q: %v", id, err)
	}
	sessions := h.list(t)
	taken := sessions[id]
	if len(sessions) != 1 || taken.Inject != protocol.InjectAPI || taken.Previous != paneID ||
		taken.Handoff != protocol.HandoffTerminal {
		t.Fatalf("taken over = %+v (listed %v)", taken, sessions)
	}
}

// Nothing is closed that could not then be carried on, nor mid-turn, nor a
// terminal of the user's own.
func TestKiroTakeOverRefusals(t *testing.T) {
	t.Run("mid-turn", func(t *testing.T) {
		h := newKiroHandoffSource(t)
		h.screen = "Kiro is working · esc to cancel\n"
		h.open(t, "agentman-kiro-1790528359000-ab12", t.TempDir())
		h.list(t)
		if _, err := h.ReleasePane(context.Background(), "kiro:tmux-agentman-kiro-1790528359000-ab12"); !errors.Is(err, ErrKiroBusy) || len(h.killed) != 0 {
			t.Fatalf("err = %v, closed %q", err, h.killed)
		}
	})
	t.Run("no Kiro to run it over ACP", func(t *testing.T) {
		h := newKiroHandoffSource(t)
		h.acpDial = nil
		previous := kiroACPInstalled
		kiroACPInstalled = func() bool { return false }
		t.Cleanup(func() { kiroACPInstalled = previous })
		h.open(t, "agentman-kiro-1790528359000-ab12", t.TempDir())
		if session := h.list(t)["kiro:tmux-agentman-kiro-1790528359000-ab12"]; session.Handoff != "" {
			t.Fatalf("offered %q", session.Handoff)
		}
		if _, err := h.ReleasePane(context.Background(), "kiro:tmux-agentman-kiro-1790528359000-ab12"); !errors.Is(err, ErrNoKiroACP) || len(h.killed) != 0 {
			t.Fatalf("err = %v, closed %q", err, h.killed)
		}
	})
	t.Run("the user's own terminal", func(t *testing.T) {
		h := newKiroHandoffSource(t)
		writeKiroSession(t, h.home, fakeKiroSession, t.TempDir(), os.Getpid(), kiroLinePrompt, kiroLineReply)
		session := h.list(t)["kiro:"+fakeKiroSession]
		if session.Inject != protocol.InjectNone || session.Handoff != "" {
			t.Fatalf("their own terminal = %+v", session)
		}
		if _, err := h.ReleasePane(context.Background(), session.ID); err == nil || len(h.killed) != 0 {
			t.Fatalf("err = %v, closed %q", err, h.killed)
		}
	})
}
