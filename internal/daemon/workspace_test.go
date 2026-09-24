package daemon

import (
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func workspaceDaemon(dir string) *Daemon {
	return &Daemon{
		sessions: map[string]protocol.Session{
			"codex:test": {ID: "codex:test", Cwd: dir},
		},
		seen: newSeenPaths(),
	}
}

func TestWorkspaceReadsAnImageWithoutExposingItAsText(t *testing.T) {
	dir := t.TempDir()
	file, err := os.Create(filepath.Join(dir, "preview.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	d := workspaceDaemon(dir)
	result := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqReadFile, SessionID: "codex:test", Path: "preview.png"})
	if result.Type != protocol.EvtWorkspace || result.Workspace.MIME != "image/png" || result.Workspace.Text != "" {
		t.Fatalf("image: %+v", result)
	}
	if _, err := base64.StdEncoding.DecodeString(result.Workspace.Image); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceConfinesReadsAndHidesPrivateFiles(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("token=secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "alias.go")); err != nil {
		t.Fatal(err)
	}
	d := workspaceDaemon(dir)
	list := d.workspace(context.Background(), protocol.Request{Type: protocol.ReqListFiles, SessionID: "codex:test"})
	if list.Type != protocol.EvtWorkspace || len(list.Workspace.Entries) != 1 || list.Workspace.Entries[0].Name != "main.go" {
		t.Fatalf("unexpected listing: %+v", list)
	}
	for _, name := range []string{"../secret.txt", ".env", "alias.go", "/etc/passwd", "foo/../../secret.txt"} {
		result := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqReadFile, SessionID: "codex:test", Path: name})
		if result.Type != protocol.EvtError {
			t.Errorf("%q escaped: %+v", name, result)
		}
	}
	read := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqReadFile, SessionID: "codex:test", Path: "main.go"})
	if read.Type != protocol.EvtWorkspace || read.Workspace.Text != "package main\n" {
		t.Fatalf("read: %+v", read)
	}
}

func TestWorkspaceShowsWorkingTreeDiffAndUntrackedFiles(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "a.go")
	git("commit", "-qm", "base")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("new\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("fresh\n"), 0600); err != nil {
		t.Fatal(err)
	}
	d := workspaceDaemon(dir)
	changes := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqListChanges, SessionID: "codex:test"})
	if changes.Type != protocol.EvtWorkspace || len(changes.Workspace.Changes) != 2 {
		t.Fatalf("changes: %+v", changes)
	}
	diff := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqFileDiff, SessionID: "codex:test", Path: "a.go"})
	if diff.Type != protocol.EvtWorkspace || !strings.Contains(diff.Workspace.Diff, "+new") || !strings.Contains(diff.Workspace.Diff, "-old") {
		t.Fatalf("diff: %+v", diff)
	}
	untracked := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqFileDiff, SessionID: "codex:test", Path: "b.go"})
	if untracked.Type != protocol.EvtWorkspace || !strings.Contains(untracked.Workspace.Diff, "+fresh") {
		t.Fatalf("untracked diff: %+v", untracked)
	}
}

func TestWorkspaceChangesStayRelativeToNestedSessionDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "app")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(sub, "main.ts"), []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "base")
	if err := os.WriteFile(filepath.Join(sub, "main.ts"), []byte("after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outside.ts"), []byte("outside\n"), 0600); err != nil {
		t.Fatal(err)
	}
	d := workspaceDaemon(sub)
	changes := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqListChanges, SessionID: "codex:test"})
	if changes.Type != protocol.EvtWorkspace || len(changes.Workspace.Changes) != 1 || changes.Workspace.Changes[0].Path != "main.ts" {
		t.Fatalf("nested changes: %+v", changes)
	}
}

func TestWorkspaceChangesHideSymlinks(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("visible.txt", filepath.Join(dir, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	changes := workspaceDaemon(dir).Handle(context.Background(), protocol.Request{
		Type: protocol.ReqListChanges, SessionID: "codex:test",
	})
	if changes.Type != protocol.EvtWorkspace || len(changes.Workspace.Changes) != 1 ||
		changes.Workspace.Changes[0].Path != "visible.txt" {
		t.Fatalf("symlink appeared in changes: %+v", changes)
	}
}

func TestWorkspaceDiffBeforeFirstCommitIncludesUnstagedEdits(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	file := filepath.Join(dir, "first.txt")
	if err := os.WriteFile(file, []byte("staged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "first.txt")
	if err := os.WriteFile(file, []byte("current\n"), 0600); err != nil {
		t.Fatal(err)
	}
	diff := workspaceDaemon(dir).Handle(context.Background(), protocol.Request{
		Type: protocol.ReqFileDiff, SessionID: "codex:test", Path: "first.txt",
	})
	if diff.Type != protocol.EvtWorkspace || !strings.Contains(diff.Workspace.Diff, "+current") ||
		strings.Contains(diff.Workspace.Diff, "+staged") {
		t.Fatalf("first commit diff: %+v", diff)
	}
}

// gitRepo makes a directory git can report on, so listChanges has something
// real to read rather than a stub.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v %s", err, out)
		}
	}
	return dir
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Regression: every segment beginning with a dot was private, against a nine
// name allowlist. An agent working in .agents or .claude produced a changes
// screen that said there was nothing to review.
func TestWorkspaceListsChangesInsideDotFolders(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, ".agents/skills/search/cli.ts", "export const go = 1;\n")
	write(t, dir, ".claude/settings.json", "{}\n")
	write(t, dir, ".vscode/launch.json", "{}\n")
	write(t, dir, "main.go", "package main\n")

	d := workspaceDaemon(dir)
	event := d.workspace(context.Background(), protocol.Request{Type: protocol.ReqListChanges, SessionID: "codex:test"})
	if event.Type != protocol.EvtWorkspace {
		t.Fatalf("unexpected event: %+v", event)
	}
	seen := map[string]bool{}
	for _, change := range event.Workspace.Changes {
		seen[change.Path] = true
	}
	for _, want := range []string{
		".agents/skills/search/cli.ts", ".claude/settings.json",
		".vscode/launch.json", "main.go",
	} {
		if !seen[want] {
			t.Errorf("%s was hidden; got %v", want, seen)
		}
	}
}

func TestWorkspaceStillHidesCredentialsInDotFolders(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, ".env", "TOKEN=live\n")
	write(t, dir, ".env.local", "TOKEN=live\n")
	write(t, dir, ".ssh/id_rsa", "-----BEGIN-----\n")
	write(t, dir, ".aws/config", "[default]\n")
	write(t, dir, ".docker/config.json", "{}\n")
	write(t, dir, ".npmrc", "//registry:_authToken=x\n")
	write(t, dir, ".git-credentials", "https://x:y@github.com\n")
	write(t, dir, "safe.go", "package main\n")

	d := workspaceDaemon(dir)
	event := d.workspace(context.Background(), protocol.Request{Type: protocol.ReqListChanges, SessionID: "codex:test"})
	for _, change := range event.Workspace.Changes {
		if change.Path != "safe.go" {
			t.Errorf("%s should not have been listed", change.Path)
		}
	}
	if event.Workspace.Hidden == 0 {
		t.Error("withheld files were not counted, so the app cannot say so")
	}
	for _, name := range []string{
		".env", ".env.local", ".ssh/id_rsa", ".aws/config",
		".docker/config.json", ".npmrc", ".git-credentials",
	} {
		result := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqReadFile, SessionID: "codex:test", Path: name})
		if result.Type != protocol.EvtError {
			t.Errorf("%q was readable: %+v", name, result)
		}
	}
}

func TestWorkspaceCountsWhatItHidesWhenListingFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "main.go", "package main\n")
	write(t, dir, ".env", "TOKEN=live\n")
	write(t, dir, ".vscode/launch.json", "{}\n")

	d := workspaceDaemon(dir)
	event := d.workspace(context.Background(), protocol.Request{Type: protocol.ReqListFiles, SessionID: "codex:test"})
	names := map[string]bool{}
	for _, entry := range event.Workspace.Entries {
		names[entry.Name] = true
	}
	if !names["main.go"] || !names[".vscode"] {
		t.Errorf("expected main.go and .vscode, got %v", names)
	}
	if names[".env"] {
		t.Error(".env was listed")
	}
	if event.Workspace.Hidden != 1 {
		t.Errorf("Hidden = %d, want 1", event.Workspace.Hidden)
	}
}
