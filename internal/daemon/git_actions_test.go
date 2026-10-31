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

// writingAgentman writes every commit message the same, and says what it
// was shown.
type writingAgentman struct {
	switchedAgentman
	shown *string
}

func (a writingAgentman) CommitMessage(_ context.Context, diff string) (string, error) {
	*a.shown = diff
	return "Describe the change", nil
}

// gitActionRepo is a repository on main with one commit, pushed to a bare
// origin, and the user's own git config kept out of the way.
func gitActionRepo(t *testing.T) (dir, origin string, git func(args ...string) string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	base := t.TempDir()
	dir, origin = filepath.Join(base, "work"), filepath.Join(base, "origin.git")
	run := func(in string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", in}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run(base, "init", "-q", "--bare", "-b", "main", origin)
	run(dir, "init", "-q", "-b", "main")
	run(dir, "config", "user.email", "test@example.com")
	run(dir, "config", "user.name", "Test")
	run(dir, "config", "commit.gpgsign", "false")
	write(t, dir, "a.txt", "a\n")
	write(t, dir, "b.txt", "b\n")
	write(t, dir, "d.txt", "d\n")
	run(dir, "add", ".")
	run(dir, "commit", "-qm", "base")
	run(dir, "remote", "add", "origin", origin)
	run(dir, "push", "-q", "-u", "origin", "main")
	return dir, origin, func(args ...string) string { return run(dir, args...) }
}

func TestGitStatusReadsWhereTheBranchStands(t *testing.T) {
	dir, _, git := gitActionRepo(t)
	git("checkout", "-q", "-b", "feature")
	write(t, dir, "a.txt", "a2\n")
	git("commit", "-qam", "change a")
	write(t, dir, "new.txt", "new\n")

	event := workspaceDaemon(dir).Handle(context.Background(), protocol.Request{Type: protocol.ReqGitStatus, SessionID: "codex:test"})
	if event.Type != protocol.EvtGit || event.Git == nil {
		t.Fatalf("unexpected event: %+v", event)
	}
	state := *event.Git
	if state.Branch != "feature" || state.DefaultBranch != "main" || state.Remote != "origin" {
		t.Errorf("branch %q default %q remote %q", state.Branch, state.DefaultBranch, state.Remote)
	}
	if state.Upstream != "" || state.Ahead != 1 || state.AheadOfDefault != 1 {
		t.Errorf("before its first push: upstream %q ahead %d of main %d", state.Upstream, state.Ahead, state.AheadOfDefault)
	}
	if state.Changes != 1 {
		t.Errorf("changes %d, want the one new file", state.Changes)
	}
}

func TestCommitTakesOnlyTheFilesNamed(t *testing.T) {
	dir, _, git := gitActionRepo(t)
	write(t, dir, "a.txt", "a2\n")
	write(t, dir, "b.txt", "b2\n")
	write(t, dir, "c.txt", "c\n")
	write(t, dir, "d.txt", "d2\n")
	git("add", "d.txt")

	event := workspaceDaemon(dir).Handle(context.Background(), protocol.Request{
		Type: protocol.ReqGitCommit, SessionID: "codex:test", Text: "Change a and add c", Paths: []string{"a.txt", "c.txt"},
	})
	if event.Type != protocol.EvtGit || event.Git.Done != "committed" || event.Git.Commit == "" {
		t.Fatalf("unexpected event: %+v %+v", event, event.Git)
	}
	if files := git("show", "--name-only", "--format=%s", "HEAD"); files != "Change a and add c\n\na.txt\nc.txt" {
		t.Errorf("the commit:\n%s", files)
	}
	// What wasn't named stays as it was: b changed, d staged.
	if status := git("status", "--porcelain"); status != "M b.txt\nM  d.txt" {
		t.Errorf("left behind:\n%q", status)
	}
}

func TestCommitWithoutAMessageHasAgentmanWriteOne(t *testing.T) {
	dir, _, git := gitActionRepo(t)
	write(t, dir, "a.txt", "a2\n")
	write(t, dir, "c.txt", "c\n")

	d := workspaceDaemon(dir)
	request := protocol.Request{Type: protocol.ReqGitCommit, SessionID: "codex:test", Paths: []string{"a.txt", "c.txt"}}
	if event := d.Handle(context.Background(), request); event.Type != protocol.EvtError || !strings.Contains(event.Error, "Agentman") {
		t.Fatalf("without Agentman: %+v", event)
	}
	if status := git("status", "--porcelain"); status != "M a.txt\n?? c.txt" {
		t.Errorf("a failed commit staged something:\n%q", status)
	}

	var shown string
	d.SetManager(writingAgentman{shown: &shown})
	event := d.Handle(context.Background(), request)
	if event.Type != protocol.EvtGit || event.Git.Message != "Describe the change" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if subject := git("log", "-1", "--format=%s"); subject != "Describe the change" {
		t.Errorf("committed as %q", subject)
	}
	if !strings.Contains(shown, "+a2") || !strings.Contains(shown, "new file: c.txt") {
		t.Errorf("Agentman was shown:\n%s", shown)
	}
}

func TestPushSetsTheUpstreamTheFirstTime(t *testing.T) {
	dir, origin, git := gitActionRepo(t)
	git("checkout", "-q", "-b", "feature")
	write(t, dir, "a.txt", "a2\n")
	git("commit", "-qam", "change a")

	event := workspaceDaemon(dir).Handle(context.Background(), protocol.Request{Type: protocol.ReqGitPush, SessionID: "codex:test"})
	if event.Type != protocol.EvtGit || event.Git.Done != "pushed" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.Git.Upstream != "origin/feature" || event.Git.Ahead != 0 {
		t.Errorf("after the push: upstream %q ahead %d", event.Git.Upstream, event.Git.Ahead)
	}
	out, err := exec.Command("git", "-C", origin, "rev-parse", "feature").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != git("rev-parse", "HEAD") {
		t.Errorf("origin's feature: %s %v", out, err)
	}
}

func TestAFailedPushSaysWhy(t *testing.T) {
	dir, _, git := gitActionRepo(t)
	git("remote", "set-url", "origin", filepath.Join(dir, "missing.git"))
	write(t, dir, "a.txt", "a2\n")
	git("commit", "-qam", "change a")

	event := workspaceDaemon(dir).Handle(context.Background(), protocol.Request{Type: protocol.ReqGitPush, SessionID: "codex:test"})
	if event.Type != protocol.EvtError || !strings.Contains(event.Error, "missing.git") {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestCreatePRNeedsTheGitHubCLI(t *testing.T) {
	dir, _, git := gitActionRepo(t)
	git("checkout", "-q", "-b", "feature")
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	bin := t.TempDir()
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	event := workspaceDaemon(dir).Handle(context.Background(), protocol.Request{Type: protocol.ReqGitCreatePR, SessionID: "codex:test"})
	if event.Type != protocol.EvtError || !strings.Contains(event.Error, "gh auth login") {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestCommitRefusesPathsOutsideTheFolder(t *testing.T) {
	for _, paths := range [][]string{nil, {"../outside.txt"}, {"/etc/passwd"}} {
		if err := validateRequest(protocol.Request{Type: protocol.ReqGitCommit, SessionID: "codex:test", Paths: paths}); err == nil {
			t.Errorf("%v was accepted", paths)
		}
	}
	if err := validateRequest(protocol.Request{Type: protocol.ReqGitCommit, SessionID: "codex:test", Paths: []string{"docs/a.md"}}); err != nil {
		t.Errorf("a plain path was refused: %v", err)
	}
}
