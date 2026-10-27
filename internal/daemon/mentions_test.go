package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func TestFilesRankAsTheEditorRanksThem(t *testing.T) {
	files := []string{
		"internal/daemon/daemon.go",
		"mobile/app/session/[id].tsx",
		"mobile/components/SessionCard.tsx",
		"docs/session-notes.md",
		"README.md",
		"internal/source/claude_session.go",
	}
	got := rankFiles("session", files, 10)
	// The name holding the word, at a word's start, shortest path first.
	if got[0] != "docs/session-notes.md" {
		t.Errorf("first = %q in %v", got[0], got)
	}
	if !slices.Contains(got, "mobile/app/session/[id].tsx") {
		t.Errorf("a folder of the name matches too: %v", got)
	}
	if slices.Contains(got, "README.md") {
		t.Errorf("README.md has no s-e-s-s-i-o-n: %v", got)
	}
	// Scattered letters: d-d for daemon.go's name.
	if got := rankFiles("dmgo", files, 10); len(got) == 0 || got[0] != "internal/daemon/daemon.go" {
		t.Errorf("dmgo = %v", got)
	}
	if got := rankFiles("zzz", files, 10); len(got) != 0 {
		t.Errorf("zzz = %v", got)
	}
}

func TestFindFilesUsesGitAndOffersChangesFirst(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(path, body string) {
		t.Helper()
		full := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write(".gitignore", "node_modules/\n")
	write("app/login.tsx", "x")
	write("app/logout.tsx", "x")
	write("node_modules/left-pad/index.js", "x")
	git("add", ".")
	git("commit", "-q", "-m", "first")
	write("app/login.tsx", "changed")
	write("notes/login-bug.md", "new")

	d := workspaceDaemon(repo)
	ask := func(query string) []string {
		t.Helper()
		event := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqFindFiles, SessionID: "codex:test", Text: query})
		if event.Type != protocol.EvtFiles {
			t.Fatalf("find %q: %+v", query, event)
		}
		return event.Files
	}
	if got := ask("login"); len(got) < 2 || got[0] != "app/login.tsx" || !slices.Contains(got, "notes/login-bug.md") {
		t.Errorf("login = %v", got)
	}
	for _, path := range ask("index") {
		if path == "node_modules/left-pad/index.js" {
			t.Errorf("an ignored file was offered: %v", path)
		}
	}
	// Nothing typed yet: what changed, and what is new.
	got := ask("")
	slices.Sort(got)
	if !slices.Equal(got, []string{"app/login.tsx", "notes/login-bug.md"}) {
		t.Errorf("changed = %v", got)
	}
	if event := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqFindFiles, SessionID: "codex:test", Text: "a\x1bb"}); event.Type != protocol.EvtError {
		t.Errorf("a control character was looked for: %+v", event)
	}
}

func TestFindFilesWalksAFolderOutsideGit(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{"plan.md", "src/plan_test.go", "node_modules/plan/index.js", ".cache/plan.txt"} {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := rankFiles("plan", folderFiles(context.Background(), dir), 10)
	if !slices.Equal(got, []string{"plan.md", "src/plan_test.go"}) {
		t.Errorf("plan = %v", got)
	}
}
