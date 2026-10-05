package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
	"github.com/lenajeremy/agentman/internal/tmux"
)

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
