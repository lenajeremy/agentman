package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// liveSource reports one session exactly as discovery found it.
type liveSource struct {
	streamingSource
	session protocol.Session
}

func (s *liveSource) Kind() protocol.Kind { return s.session.Kind }

func (s *liveSource) Discover(context.Context) ([]protocol.Session, error) {
	return []protocol.Session{s.session}, nil
}

// A session running in someone's own terminal is live, but Agentman cannot
// type into it. "Reopening" it used to start a second agent process on the same
// conversation beside the first. It must be refused instead.
func TestResumeRefusesASessionStillRunningElsewhere(t *testing.T) {
	// Nothing can be launched: a refusal is the only way to pass, and an
	// attempt to start the CLI shows up as "is not installed".
	t.Setenv("PATH", t.TempDir())
	for _, session := range []protocol.Session{
		{ID: "kiro:abc", Kind: protocol.KindKiro, State: protocol.StateBusy, Inject: protocol.InjectNone, AgentPID: 4242},
		{ID: "antigravity:abc", Kind: protocol.KindAntigravity, State: protocol.StateIdle, Inject: protocol.InjectNone, AgentPID: 4242},
		{ID: "codex:abc", Kind: protocol.KindCodex, State: protocol.StateIdle, Inject: protocol.InjectNone},
		{ID: "claude:abc", Kind: protocol.KindClaude, State: protocol.StateIdle, Inject: protocol.InjectHook, AgentPID: 4242},
	} {
		session.NativeID, session.Cwd = "abc", t.TempDir()
		registry := source.NewRegistry()
		registry.Add(&liveSource{session: session})
		agent := New(registry, &recordingSink{})
		agent.refresh(context.Background(), true)

		reply := agent.HandleFrom(context.Background(), "phone", protocol.Request{
			Type: protocol.ReqResumeSession, SessionID: session.ID,
		})
		if reply.Type != protocol.EvtError {
			t.Errorf("%s: a session running elsewhere was reopened: %+v", session.ID, reply)
			continue
		}
		if strings.Contains(reply.Error, "not installed") {
			t.Errorf("%s: the daemon tried to start a second process: %s", session.ID, reply.Error)
		}
		if !strings.Contains(reply.Error, "running") {
			t.Errorf("%s: the refusal does not say why: %s", session.ID, reply.Error)
		}
	}
}

// fakeAgent puts a stand-in for an agent CLI first on PATH that only sleeps,
// so a resume can be launched for real on the test's private tmux server.
func fakeAgent(t *testing.T, name string) {
	t.Helper()
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+filepath.Dir(tmuxPath)+":/usr/bin:/bin")
}

func agentPanes(t *testing.T, kind string) []string {
	t.Helper()
	panes, err := tmux.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, pane := range panes {
		if strings.HasPrefix(pane.Name, tmux.Prefix+kind+"-") {
			names = append(names, pane.Name)
		}
	}
	return names
}

// Two requests to reopen one ended session — a double tap, two phones, or the
// screen opened again before the agent has registered — must start it once.
// Each used to launch its own process on the same conversation.
func TestResumingTwiceStartsOneProcess(t *testing.T) {
	fakeAgent(t, "kiro-cli")
	agent := New(source.NewRegistry(), &recordingSink{})
	agent.folders.remember([]protocol.Session{{
		ID: "kiro:abc", Kind: protocol.KindKiro, NativeID: "abc", Cwd: t.TempDir(), State: protocol.StateEnded,
	}})
	t.Cleanup(func() {
		for _, name := range agentPanes(t, "kiro") {
			_ = tmux.Kill(context.Background(), name)
		}
	})

	var wg sync.WaitGroup
	replies := make([]protocol.Event, 2)
	for i := range replies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			replies[i] = agent.HandleFrom(context.Background(), "phone", protocol.Request{
				Type: protocol.ReqResumeSession, SessionID: "kiro:abc",
			})
		}()
	}
	wg.Wait()
	// And once more after both have answered, before discovery has seen it.
	replies = append(replies, agent.HandleFrom(context.Background(), "phone", protocol.Request{
		Type: protocol.ReqResumeSession, SessionID: "kiro:abc",
	}))

	for _, reply := range replies {
		if reply.Type != protocol.EvtSessionStarted || reply.SessionID != "kiro:abc" {
			t.Errorf("resume answered %+v", reply)
		}
	}
	if panes := agentPanes(t, "kiro"); len(panes) != 1 {
		t.Fatalf("one session was reopened into %d panes: %v", len(panes), panes)
	}
}

// namingKiro keys its live sessions on their pane, as Kiro's adapter does, and
// so names its own resumes.
type namingKiro struct {
	streamingSource
	answer func(native, pane string) (string, string)
}

func (*namingKiro) Kind() protocol.Kind { return protocol.KindKiro }

func (k *namingKiro) ResumedSession(native, pane string) (string, string) {
	return k.answer(native, pane)
}

type plainKiro struct{ streamingSource }

func (*plainKiro) Kind() protocol.Kind { return protocol.KindKiro }

type openedPane struct {
	name, dir string
	argv      []string
}

// resumeWithoutTmux reopens a remembered Kiro session and reports the pane
// that would have been opened, without starting tmux: the launcher is
// replaced, and the agent binary is a file that is never run.
func resumeWithoutTmux(t *testing.T, adapter source.Source) (string, openedPane, error) {
	t.Helper()
	if !tmux.Available() {
		t.Skip("the resume path checks for tmux before naming anything")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "kiro-cli"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var opened openedPane
	previous := startResumedPane
	startResumedPane = func(_ context.Context, name, dir string, argv []string, _ string) error {
		opened = openedPane{name: name, dir: dir, argv: argv}
		return nil
	}
	t.Cleanup(func() { startResumedPane = previous })

	d, _ := agentWithSource(t, adapter)
	dir := t.TempDir()
	d.folders.remember([]protocol.Session{{
		ID: "kiro:abc", Kind: protocol.KindKiro, NativeID: "abc", Cwd: dir, State: protocol.StateEnded,
	}})
	id, err := d.resumeSession(context.Background(), "kiro:abc")
	if err == nil && opened.dir != dir {
		t.Errorf("reopened in %q, want the session's own directory %q", opened.dir, dir)
	}
	return id, opened, err
}

// Kiro publishes a live session under its pane, so the phone has to be sent
// to that id, not to the one the transcript has. Only the adapter knows it.
func TestAResumeIsNamedByTheAdapterThatOwnsIt(t *testing.T) {
	var offered string
	id, opened, err := resumeWithoutTmux(t, &namingKiro{answer: func(native, pane string) (string, string) {
		offered = pane
		return pane, "kiro:tmux-" + pane
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(offered, tmux.Prefix+"kiro-") || opened.name != offered {
		t.Fatalf("offered pane %q, opened %q", offered, opened.name)
	}
	if id != "kiro:tmux-"+opened.name {
		t.Fatalf("resume answered %q, want the pane-keyed id for %q", id, opened.name)
	}
	if got := strings.Join(opened.argv[1:], " "); got != "chat --resume-id abc" {
		t.Errorf("reopened with %q", got)
	}
}

// An adapter that says nothing gets the daemon's own naming, unchanged.
func TestAResumeWithoutANamerKeepsTheDaemonsNaming(t *testing.T) {
	id, opened, err := resumeWithoutTmux(t, &plainKiro{})
	if err != nil {
		t.Fatal(err)
	}
	if id != "kiro:abc" || !strings.HasPrefix(opened.name, tmux.Prefix+"kiro-") {
		t.Fatalf("resume = %q in pane %q", id, opened.name)
	}
}

// A pane name tmux would read as a window or pane target, one that is not
// ours, or an id of another agent sends the phone nowhere; the daemon keeps
// its own naming instead.
func TestAnUnusableResumeNameIsIgnored(t *testing.T) {
	for name, answer := range map[string]func(native, pane string) (string, string){
		"window separator": func(_, pane string) (string, string) { return pane + ":1", "kiro:tmux-x" },
		"pane separator":   func(_, pane string) (string, string) { return pane + ".1", "kiro:tmux-x" },
		"not ours":         func(_, _ string) (string, string) { return "work", "kiro:tmux-work" },
		"bare prefix":      func(_, _ string) (string, string) { return tmux.Prefix, "kiro:tmux-x" },
		"other agent":      func(_, pane string) (string, string) { return pane, "claude:" + pane },
		"declined":         func(_, pane string) (string, string) { return pane, "" },
	} {
		t.Run(name, func(t *testing.T) {
			id, opened, err := resumeWithoutTmux(t, &namingKiro{answer: answer})
			if err != nil {
				t.Fatal(err)
			}
			if id != "kiro:abc" || !strings.HasPrefix(opened.name, tmux.Prefix+"kiro-") ||
				strings.ContainsAny(opened.name, ":.") {
				t.Fatalf("resume = %q in pane %q", id, opened.name)
			}
		})
	}
}
