package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// historyRepo makes a repository in a temp directory, isolated from the
// machine's own Git config, and returns it with a way to run git in it.
func historyRepo(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_DATE=2026-10-01T12:00:00Z", "GIT_COMMITTER_DATE=2026-10-01T12:00:00Z")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test Author")
	return dir, git
}

func listCommitsFor(t *testing.T, d *Daemon, offset int64) *protocol.WorkspaceResult {
	t.Helper()
	event := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqListCommits, SessionID: "codex:test", Offset: offset})
	if event.Type != protocol.EvtWorkspace || event.Workspace.Kind != "commits" {
		t.Fatalf("list_commits: %+v", event)
	}
	return event.Workspace
}

func TestCommitsListNewestFirstWithTheirSize(t *testing.T) {
	dir, git := historyRepo(t)
	write(t, dir, "a.go", "one\n")
	git("add", ".")
	git("commit", "-qm", "First")
	write(t, dir, "a.go", "one\ntwo\nthree\n")
	write(t, dir, "b.go", "bee\n")
	git("add", ".")
	git("commit", "-qm", "Second")

	result := listCommitsFor(t, workspaceDaemon(dir), 0)
	if result.Branch != "main" || result.Truncated || len(result.Commits) != 2 {
		t.Fatalf("history: %+v", result)
	}
	newest := result.Commits[0]
	if newest.Subject != "Second" || newest.Author != "Test Author" || newest.AuthoredAt != 1790856000000 ||
		newest.Files != 2 || newest.Added != 3 || newest.Removed != 0 || newest.Merge || newest.Body != "" {
		t.Fatalf("newest: %+v", newest)
	}
	if newest.Hash != git("rev-parse", "HEAD") || result.Commits[1].Subject != "First" {
		t.Fatalf("order: %+v", result.Commits)
	}
}

func TestCommitsPageThroughLongHistory(t *testing.T) {
	dir, git := historyRepo(t)
	for i := range commitPage + 5 {
		git("commit", "-q", "--allow-empty", "-m", fmt.Sprintf("Commit %d", i))
	}
	d := workspaceDaemon(dir)
	first := listCommitsFor(t, d, 0)
	if len(first.Commits) != commitPage || !first.Truncated || first.Commits[0].Subject != fmt.Sprintf("Commit %d", commitPage+4) {
		t.Fatalf("first page: %d commits, more %v", len(first.Commits), first.Truncated)
	}
	second := listCommitsFor(t, d, commitPage)
	if len(second.Commits) != 5 || second.Truncated || second.Commits[4].Subject != "Commit 0" {
		t.Fatalf("second page: %+v", second)
	}
}

func TestCommitsOfAnEmptyRepositoryAreNone(t *testing.T) {
	dir, _ := historyRepo(t)
	result := listCommitsFor(t, workspaceDaemon(dir), 0)
	if len(result.Commits) != 0 || result.Truncated || result.Branch != "main" {
		t.Fatalf("empty repository: %+v", result)
	}
}

func TestCommitsOutsideAGitRepositoryExplainThemselves(t *testing.T) {
	event := workspaceDaemon(t.TempDir()).Handle(context.Background(), protocol.Request{Type: protocol.ReqListCommits, SessionID: "codex:test"})
	if event.Type != protocol.EvtError || !strings.Contains(event.Error, "not a Git working tree") {
		t.Fatalf("outside a repository: %+v", event)
	}
}

func TestCommitFilesSayWhatChangedAndWithholdPrivateOnes(t *testing.T) {
	dir, git := historyRepo(t)
	write(t, dir, "kept.go", "a\nb\nc\n")
	write(t, dir, "gone.go", "bye\n")
	git("add", ".")
	git("commit", "-qm", "Base")
	write(t, dir, "kept.go", "a\nB\nc\nd\n")
	write(t, dir, "fresh.go", "hello\n")
	write(t, dir, ".env", "TOKEN=secret\n")
	write(t, dir, "config/secrets.yml", "key: value\n")
	if err := os.Remove(filepath.Join(dir, "gone.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("kept.go", filepath.Join(dir, "alias.go")); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "Rework kept\n\nWhy it changed,\nover two lines.\x1fwith a separator")
	hash := git("rev-parse", "HEAD")

	event := workspaceDaemon(dir).Handle(context.Background(), protocol.Request{Type: protocol.ReqCommitFiles, SessionID: "codex:test", Commit: hash})
	if event.Type != protocol.EvtWorkspace || event.Workspace.Kind != "commit" {
		t.Fatalf("commit_files: %+v", event)
	}
	result := event.Workspace
	commit := result.Commit
	if commit == nil || commit.Hash != hash || commit.Subject != "Rework kept" ||
		commit.Body != "Why it changed,\nover two lines.\x1fwith a separator" {
		t.Fatalf("commit: %+v", commit)
	}
	got := map[string]protocol.WorkspaceChange{}
	for _, change := range result.Changes {
		got[change.Path] = change
	}
	if len(got) != 3 || result.Hidden != 3 {
		t.Fatalf("changes %+v, hidden %d", result.Changes, result.Hidden)
	}
	if c := got["kept.go"]; c.Status != "M" || c.Added != 2 || c.Removed != 1 {
		t.Errorf("kept.go: %+v", c)
	}
	if c := got["fresh.go"]; c.Status != "A" || c.Added != 1 {
		t.Errorf("fresh.go: %+v", c)
	}
	if c := got["gone.go"]; c.Status != "D" || c.Removed != 1 {
		t.Errorf("gone.go: %+v", c)
	}
	if commit.Files != 3 || commit.Added != 3 || commit.Removed != 2 {
		t.Errorf("totals cover only what is shown: %+v", commit)
	}
}

func TestCommitDiffShowsOneFileAgainstTheParent(t *testing.T) {
	dir, git := historyRepo(t)
	write(t, dir, "a.go", "old\n")
	write(t, dir, "b.go", "untouched\n")
	git("add", ".")
	git("commit", "-qm", "Base")
	root := git("rev-parse", "HEAD")
	write(t, dir, "a.go", "new\n")
	write(t, dir, "b.go", "changed too\n")
	git("commit", "-qam", "Edit")
	head := git("rev-parse", "HEAD")
	// The working tree moving on afterwards must not leak into history.
	write(t, dir, "a.go", "later\n")

	d := workspaceDaemon(dir)
	event := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqCommitDiff, SessionID: "codex:test", Commit: head, Path: "a.go"})
	if event.Type != protocol.EvtWorkspace || event.Workspace.Kind != "diff" {
		t.Fatalf("commit_diff: %+v", event)
	}
	if event.Workspace.Text != "" {
		t.Fatalf("the file sent unasked: %q", event.Workspace.Text)
	}
	withFile := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqCommitDiff, SessionID: "codex:test", Commit: head, Path: "a.go", WithFile: true})
	if withFile.Type != protocol.EvtWorkspace || withFile.Workspace.Text != "new\n" {
		t.Fatalf("historical context: %+v", withFile)
	}
	diff := event.Workspace.Diff
	if !strings.Contains(diff, "-old") || !strings.Contains(diff, "+new") ||
		strings.Contains(diff, "later") || strings.Contains(diff, "b.go") || strings.Contains(diff, "\x1b[") {
		t.Fatalf("diff: %q", diff)
	}
	first := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqCommitDiff, SessionID: "codex:test", Commit: root, Path: "a.go"})
	if first.Type != protocol.EvtWorkspace || !strings.Contains(first.Workspace.Diff, "@@ -0,0 +1 @@") ||
		!strings.Contains(first.Workspace.Diff, "+old") {
		t.Fatalf("root commit diff: %+v", first)
	}
}

// A commit's diff can leave out lines whose only change is whitespace, as
// the uncommitted diff can.
func TestCommitDiffCanIgnoreWhitespace(t *testing.T) {
	dir, git := historyRepo(t)
	write(t, dir, "a.go", "func a() {\nreturn 1\n}\n")
	git("add", ".")
	git("commit", "-qm", "Base")
	write(t, dir, "a.go", "func a() {\n\treturn 1\n}\n// done\n")
	git("commit", "-qam", "Indent, and a comment")
	head := git("rev-parse", "HEAD")

	d := workspaceDaemon(dir)
	diff := func(ignore bool) string {
		event := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqCommitDiff, SessionID: "codex:test",
			Commit: head, Path: "a.go", IgnoreWhitespace: ignore})
		if event.Type != protocol.EvtWorkspace {
			t.Fatalf("commit_diff: %+v", event)
		}
		return event.Workspace.Diff
	}
	if all := diff(false); !strings.Contains(all, "+\treturn 1") || !strings.Contains(all, "+// done") {
		t.Fatalf("whole diff: %q", all)
	}
	if some := diff(true); strings.Contains(some, "return 1\n+") || strings.Contains(some, "+\treturn 1") || !strings.Contains(some, "+// done") {
		t.Fatalf("whitespace left in: %q", some)
	}
}

func TestCommitsStayWithinANestedSessionDirectory(t *testing.T) {
	dir, git := historyRepo(t)
	write(t, dir, "app/main.ts", "one\n")
	write(t, dir, "outside.ts", "one\n")
	git("add", ".")
	git("commit", "-qm", "Both")
	write(t, dir, "outside.ts", "two\n")
	git("commit", "-qam", "Outside only")
	write(t, dir, "app/main.ts", "two\n")
	write(t, dir, "outside.ts", "three\n")
	git("commit", "-qam", "Both again")

	d := workspaceDaemon(filepath.Join(dir, "app"))
	result := listCommitsFor(t, d, 0)
	if len(result.Commits) != 2 || result.Commits[0].Subject != "Both again" || result.Commits[1].Subject != "Both" {
		t.Fatalf("nested history: %+v", result.Commits)
	}
	if c := result.Commits[0]; c.Files != 1 || c.Added != 1 || c.Removed != 1 {
		t.Fatalf("nested counts: %+v", c)
	}
	files := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqCommitFiles, SessionID: "codex:test", Commit: result.Commits[0].Hash})
	if files.Type != protocol.EvtWorkspace || len(files.Workspace.Changes) != 1 || files.Workspace.Changes[0].Path != "main.ts" {
		t.Fatalf("nested files: %+v", files)
	}
	diff := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqCommitDiff, SessionID: "codex:test", Commit: result.Commits[0].Hash, Path: "main.ts"})
	if diff.Type != protocol.EvtWorkspace || !strings.Contains(diff.Workspace.Diff, "+two") || strings.Contains(diff.Workspace.Diff, "outside") {
		t.Fatalf("nested diff: %+v", diff)
	}
}

func TestAMergeIsMeasuredAgainstItsFirstParent(t *testing.T) {
	dir, git := historyRepo(t)
	write(t, dir, "base.go", "base\n")
	git("add", ".")
	git("commit", "-qm", "Base")
	git("switch", "-q", "-c", "side")
	write(t, dir, "side.go", "one\ntwo\n")
	git("add", ".")
	git("commit", "-qm", "Side work")
	git("switch", "-q", "main")
	write(t, dir, "main.go", "main\n")
	git("add", ".")
	git("commit", "-qm", "Main work")
	git("merge", "-q", "--no-ff", "-m", "Merge side", "side")

	d := workspaceDaemon(dir)
	result := listCommitsFor(t, d, 0)
	merge := result.Commits[0]
	if len(result.Commits) != 4 || !merge.Merge || merge.Subject != "Merge side" || merge.Files != 1 || merge.Added != 2 {
		t.Fatalf("merge: %+v", result.Commits)
	}
	files := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqCommitFiles, SessionID: "codex:test", Commit: merge.Hash})
	if files.Type != protocol.EvtWorkspace || len(files.Workspace.Changes) != 1 || files.Workspace.Changes[0].Path != "side.go" {
		t.Fatalf("merge files: %+v", files)
	}
}

func TestCommitRequestsRefuseAnythingButAFullHash(t *testing.T) {
	dir, git := historyRepo(t)
	write(t, dir, "a.go", "a\n")
	write(t, dir, ".env", "TOKEN=secret\n")
	git("add", ".")
	git("commit", "-qm", "Base")
	hash := git("rev-parse", "HEAD")
	d := workspaceDaemon(dir)
	for _, commit := range []string{"", "HEAD", hash[:7], strings.ToUpper(hash), "--output=/tmp/x", hash + "^", hash + " " + hash} {
		event := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqCommitFiles, SessionID: "codex:test", Commit: commit})
		if event.Type != protocol.EvtError {
			t.Errorf("%q was accepted: %+v", commit, event)
		}
	}
	missing := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqCommitFiles, SessionID: "codex:test", Commit: strings.Repeat("a", 40)})
	if missing.Type != protocol.EvtError || !strings.Contains(missing.Error, "not in this repository") {
		t.Errorf("unknown commit: %+v", missing)
	}
	// A blob or a tree is not a commit, even though its hash has the shape of one.
	tree := git("rev-parse", "HEAD^{tree}")
	if event := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqCommitFiles, SessionID: "codex:test", Commit: tree}); event.Type != protocol.EvtError {
		t.Errorf("a tree was read as a commit: %+v", event)
	}
	for _, path := range []string{".env", "../a.go", "/etc/passwd", ""} {
		event := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqCommitDiff, SessionID: "codex:test", Commit: hash, Path: path})
		if event.Type != protocol.EvtError {
			t.Errorf("diff of %q was served: %+v", path, event)
		}
	}
	if event := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqListCommits, SessionID: "codex:test", Offset: -1}); event.Type != protocol.EvtError {
		t.Errorf("negative offset accepted: %+v", event)
	}
}

func TestBranchesListLocalAndRemoteNewestFirst(t *testing.T) {
	dir, git := historyRepo(t)
	write(t, dir, "a.go", "a\n")
	git("add", ".")
	git("commit", "-qm", "Base")
	git("switch", "-q", "-c", "feature")
	git("commit", "-q", "--allow-empty", "-m", "Feature work")
	git("switch", "-q", "main")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	event := workspaceDaemon(dir).Handle(context.Background(), protocol.Request{Type: protocol.ReqListBranches, SessionID: "codex:test"})
	if event.Type != protocol.EvtWorkspace || event.Workspace.Kind != "branches" {
		t.Fatalf("list_branches: %+v", event)
	}
	got := map[string]protocol.WorkspaceBranch{}
	for _, branch := range event.Workspace.Branches {
		got[branch.Name] = branch
	}
	if len(got) != 3 {
		t.Fatalf("origin/HEAD should be left out, it only points at origin/main: %+v", event.Workspace.Branches)
	}
	if b := got["main"]; b.Ref != "refs/heads/main" || !b.Current || b.Remote || b.UpdatedAt != 1790856000000 {
		t.Errorf("main: %+v", b)
	}
	if b := got["feature"]; b.Ref != "refs/heads/feature" || b.Current || b.Remote {
		t.Errorf("feature: %+v", b)
	}
	if b := got["origin/main"]; b.Ref != "refs/remotes/origin/main" || !b.Remote || b.Current {
		t.Errorf("origin/main: %+v", b)
	}
}

// The point of reading a branch's history from the phone is that it leaves
// the agent's working directory exactly as it was: same branch, same index,
// same uncommitted edits.
func TestABranchsHistoryIsReadWithoutCheckingItOut(t *testing.T) {
	dir, git := historyRepo(t)
	write(t, dir, "a.go", "base\n")
	git("add", ".")
	git("commit", "-qm", "Base")
	git("switch", "-q", "-c", "feature")
	write(t, dir, "a.go", "feature\n")
	git("commit", "-qam", "Feature work")
	git("switch", "-q", "main")
	// An agent mid-task: one edit staged, one not.
	write(t, dir, "a.go", "agent is editing\n")
	write(t, dir, "new.go", "untracked\n")
	git("add", "new.go")
	before := git("status", "--porcelain=v1", "--branch")
	head := git("rev-parse", "HEAD")

	d := workspaceDaemon(dir)
	event := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqListCommits, SessionID: "codex:test", Ref: "refs/heads/feature"})
	if event.Type != protocol.EvtWorkspace || len(event.Workspace.Commits) != 2 ||
		event.Workspace.Commits[0].Subject != "Feature work" || event.Workspace.Branch != "main" {
		t.Fatalf("feature history: %+v", event)
	}
	if after := git("status", "--porcelain=v1", "--branch"); after != before {
		t.Fatalf("reading a branch changed the working tree:\nbefore %q\nafter  %q", before, after)
	}
	if git("rev-parse", "HEAD") != head {
		t.Fatal("reading a branch moved HEAD")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a.go")); string(data) != "agent is editing\n" {
		t.Fatalf("reading a branch rewrote a file: %q", data)
	}
	// Paging goes on reading the same branch.
	page := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqListCommits, SessionID: "codex:test", Ref: "refs/heads/feature", Offset: 1})
	if page.Type != protocol.EvtWorkspace || len(page.Workspace.Commits) != 1 || page.Workspace.Commits[0].Subject != "Base" {
		t.Fatalf("second page of feature: %+v", page)
	}
}

func TestABranchNamedLikeAFileIsStillABranch(t *testing.T) {
	dir, git := historyRepo(t)
	write(t, dir, "a.go", "a\n")
	git("add", ".")
	git("commit", "-qm", "Base")
	git("branch", "docs")
	write(t, dir, "refs/heads/docs", "a file with the branch's full name\n")
	event := workspaceDaemon(dir).Handle(context.Background(), protocol.Request{Type: protocol.ReqListCommits, SessionID: "codex:test", Ref: "refs/heads/docs"})
	if event.Type != protocol.EvtWorkspace || len(event.Workspace.Commits) != 1 {
		t.Fatalf("branch read as a path: %+v", event)
	}
}

func TestOnlyAPlainBranchNameIsAccepted(t *testing.T) {
	dir, git := historyRepo(t)
	write(t, dir, "a.go", "a\n")
	git("add", ".")
	git("commit", "-qm", "Base")
	git("tag", "v1")
	d := workspaceDaemon(dir)
	for _, ref := range []string{
		"main", "HEAD", "--all", "--output=/tmp/x", "refs/tags/v1", "refs/heads/main^",
		"refs/heads/main~1", "refs/heads/main@{1}", "refs/heads/../../config", "refs/heads/a b",
		"refs/heads/main..refs/heads/main", "refs/heads/", "refs/heads/x.lock", "refs/heads/m\x00",
	} {
		event := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqListCommits, SessionID: "codex:test", Ref: ref})
		if event.Type != protocol.EvtError {
			t.Errorf("%q was accepted: %+v", ref, event)
		}
	}
	gone := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqListCommits, SessionID: "codex:test", Ref: "refs/heads/deleted"})
	if gone.Type != protocol.EvtError || !strings.Contains(gone.Error, "no longer in this repository") {
		t.Errorf("a missing branch: %+v", gone)
	}
}

func TestCommitContextStaysInTheSelectedTree(t *testing.T) {
	dir, git := historyRepo(t)
	write(t, dir, "folder/a.go", "historical\n")
	write(t, dir, "binary", "a\x00b")
	if err := os.Symlink("folder/a.go", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "Files")
	hash := git("rev-parse", "HEAD")
	write(t, dir, "folder/a.go", "working\n")
	ctx := context.Background()
	if got := commitContext(ctx, filepath.Join(dir, "folder"), hash, "a.go"); got != "historical\n" {
		t.Fatalf("subdirectory context: %q", got)
	}
	for _, name := range []string{"link", "binary", "missing"} {
		if got := commitContext(ctx, dir, hash, name); got != "" {
			t.Errorf("%s context: %q", name, got)
		}
	}
}
