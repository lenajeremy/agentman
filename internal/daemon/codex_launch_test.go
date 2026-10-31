package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/source"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// A launch Codex's daemon started opens a terminal on that conversation, in
// a pane that names it, and the phone is sent to the pane's session.
func TestACodexLaunchOpensATerminalOnTheDaemonsConversation(t *testing.T) {
	if !tmux.Available() {
		t.Skip("the resume path checks for tmux before naming anything")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	const thread = "01a19300-0000-7000-8000-0000000000aa"
	var started struct{ dir, prompt string }
	previousStart := startCodexThread
	startCodexThread = func(_ context.Context, _ *source.Registry, dir, prompt string) (string, error) {
		started.dir, started.prompt = dir, prompt
		return thread, nil
	}
	t.Cleanup(func() { startCodexThread = previousStart })
	var opened openedPane
	previousPane := startResumedPane
	startResumedPane = func(_ context.Context, name, dir string, argv []string, _ string) error {
		opened = openedPane{name: name, dir: dir, argv: argv}
		return nil
	}
	t.Cleanup(func() { startResumedPane = previousPane })

	codex, err := source.NewCodexSource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d, _ := agentWithSource(t, codex)
	dir := t.TempDir()
	id, err := d.startCodexSession(context.Background(), dir, "add a health check")
	if err != nil {
		t.Fatal(err)
	}
	if started.dir != dir || started.prompt != "add a health check" {
		t.Fatalf("started %+v", started)
	}
	if got := strings.Join(opened.argv[1:], " "); got != "resume "+thread || opened.dir != dir {
		t.Fatalf("opened %q in %q", got, opened.dir)
	}
	if !strings.HasSuffix(opened.name, "-resume-"+thread) || id != "codex:tmux-"+opened.name {
		t.Fatalf("pane %q, id %q", opened.name, id)
	}
}
