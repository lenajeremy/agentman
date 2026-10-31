package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// worktreeRepo is gitActionRepo on a branch of its own, with .env files git
// ignores at its root and in a folder, and a home of its own for
// ~/Agentman/Worktrees.
func worktreeRepo(t *testing.T) (dir, home string, git func(args ...string) string) {
	t.Helper()
	dir, _, git = gitActionRepo(t)
	home = t.TempDir()
	t.Setenv("HOME", home)
	write(t, dir, ".gitignore", ".env\n.env.*\n")
	git("add", ".gitignore")
	git("commit", "-qm", "ignore env files")
	git("checkout", "-q", "-b", "feature")
	write(t, dir, "a.txt", "on feature\n")
	git("commit", "-qam", "feature work")
	write(t, dir, ".env", "API_KEY=secret\n")
	write(t, dir, "app/.env.local", "PORT=3000\n")
	return dir, home, git
}

func TestAWorktreeStartsFromTheCheckedOutBranch(t *testing.T) {
	dir, home, git := worktreeRepo(t)
	d := workspaceDaemon(dir)
	event := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqCreateWorktree, SessionID: "codex:test", Text: "rate limits"})
	if event.Type != protocol.EvtWorktree || event.Worktree == nil {
		t.Fatalf("unexpected event: %+v", event)
	}
	wt := *event.Worktree
	want := filepath.Join(home, "Agentman", "Worktrees", "work", "rate-limits")
	if wt.Path != want || wt.Name != "rate-limits" || wt.Repo != "work" || wt.Branch != "rate-limits" || !wt.Agentman {
		t.Errorf("worktree = %+v, want it at %s", wt, want)
	}
	run := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", want}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if branch := run("branch", "--show-current"); branch != "rate-limits" {
		t.Errorf("checked out %q", branch)
	}
	if head, feature := run("rev-parse", "HEAD"), git("rev-parse", "feature"); head != feature {
		t.Errorf("it starts at %s, not feature's %s", head, feature)
	}
	if got, _ := os.ReadFile(filepath.Join(want, ".env")); string(got) != "API_KEY=secret\n" {
		t.Errorf(".env = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(want, "app", ".env.local")); string(got) != "PORT=3000\n" {
		t.Errorf("app/.env.local = %q", got)
	}
	if strings.Join(wt.Copied, ",") != ".env,app/.env.local" {
		t.Errorf("copied = %v", wt.Copied)
	}
	if git("branch", "--show-current") != "feature" {
		t.Error("the repository's own folder changed branch")
	}

	// The same name again, and a name no branch can have.
	for _, name := range []string{"rate limits", "bad..name"} {
		again := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqCreateWorktree, SessionID: "codex:test", Text: name})
		if again.Type != protocol.EvtError {
			t.Errorf("%q was made: %+v", name, again)
		}
	}

	list := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqListWorktrees, SessionID: "codex:test"})
	if list.Type != protocol.EvtWorktrees || len(list.Worktrees) != 2 {
		t.Fatalf("list = %+v", list)
	}
	if main := list.Worktrees[0]; !main.Main || main.Agentman || main.Branch != "feature" {
		t.Errorf("the repository's own folder = %+v", main)
	}
	if made := list.Worktrees[1]; made.Main || !made.Agentman || made.Branch != "rate-limits" {
		t.Errorf("the worktree = %+v", made)
	}
}

func TestRemovingAWorktreeAsksBeforeLosingWork(t *testing.T) {
	dir, home, git := worktreeRepo(t)
	d := workspaceDaemon(dir)
	if event := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqCreateWorktree, SessionID: "codex:test", Text: "docs"}); event.Type != protocol.EvtWorktree {
		t.Fatalf("create: %+v", event)
	}
	path := filepath.Join(home, "Agentman", "Worktrees", "work", "docs")
	rel := "Agentman/Worktrees/work/docs"
	remove := func(force bool) protocol.Event {
		return d.Handle(context.Background(), protocol.Request{Type: protocol.ReqRemoveWorktree, Path: rel, Force: force})
	}

	// An agent working in it.
	d.mu.Lock()
	d.sessions["claude:wt"] = protocol.Session{ID: "claude:wt", Name: "docs agent", Cwd: path, State: protocol.StateBusy}
	d.mu.Unlock()
	if event := remove(false); event.Type != protocol.EvtError || !strings.Contains(event.Error, "docs agent is still working") {
		t.Errorf("removed under a working agent: %+v", event)
	}
	d.mu.Lock()
	delete(d.sessions, "claude:wt")
	d.mu.Unlock()

	// Work not committed.
	write(t, path, "notes.md", "draft\n")
	if event := remove(false); event.Type != protocol.EvtError || !strings.Contains(event.Error, "uncommitted changes") {
		t.Errorf("removed with uncommitted work: %+v", event)
	}
	if _, err := os.Stat(filepath.Join(path, "notes.md")); err != nil {
		t.Fatal("the uncommitted work went")
	}

	event := remove(true)
	if event.Type != protocol.EvtWorktrees || len(event.Worktrees) != 1 || !event.Worktrees[0].Main {
		t.Fatalf("remove: %+v", event)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the folder is still there: %v", err)
	}
	if git("branch", "--list", "docs") == "" {
		t.Error("its branch went with it")
	}
}

func TestOnlyAgentmansWorktreesCanBeRemoved(t *testing.T) {
	dir, home, git := worktreeRepo(t)
	own := filepath.Join(home, "elsewhere")
	git("worktree", "add", "-q", "-b", "mine", own)
	d := workspaceDaemon(dir)
	event := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqRemoveWorktree, Path: "elsewhere", Force: true})
	if event.Type != protocol.EvtError || !strings.Contains(event.Error, "only worktrees Agentman made") {
		t.Errorf("removed the user's own worktree: %+v", event)
	}
	if _, err := os.Stat(own); err != nil {
		t.Errorf("the user's worktree went: %v", err)
	}
	if err := validateRequest(protocol.Request{Type: protocol.ReqRemoveWorktree, Path: "../outside"}); err == nil {
		t.Error("a path out of home was accepted")
	}
}
