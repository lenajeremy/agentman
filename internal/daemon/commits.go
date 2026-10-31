package daemon

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Commit history for a session's directory: the commits that touched it, the
// files one of them changed, and one file's diff within it.
//
// All of it is read from Git's object store rather than the working tree, so
// nothing here passes through a symlink on disk. It is narrowed the way the
// working tree is instead: paths are relative to the session's directory,
// anything outside that directory is left out, and private names are counted
// rather than shown.

const (
	// One page of history. A phone shows about ten commits at once, so this
	// is several screens, and still one quick git process.
	commitPage      = 50
	maxCommitOffset = 100_000
	maxSubjectBytes = 1024
	maxAuthorBytes  = 256
	maxBodyBytes    = 16 << 10
)

// commitHash is a full SHA-1 or SHA-256 commit hash. Only full hashes are
// accepted: they are what the listing sent, and one can never be read by Git
// as an option or a revision expression.
var commitHash = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

var shortStat = regexp.MustCompile(`(\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?`)

const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
	// Hash, parents, author, author date, subject. The body goes last on the
	// one request that wants it, so whatever it contains cannot shift the
	// fields before it.
	commitFields = "%H%x1f%P%x1f%an%x1f%at%x1f%s"
)

// gitHistoryFlags keep a user's own Git config out of output that is parsed:
// colour codes, signature checks printed into the log, and external diff
// tools would all otherwise land in what the phone receives.
var gitHistoryFlags = []string{"--no-color", "--no-ext-diff", "--no-textconv"}

func (d *Daemon) history(ctx context.Context, req protocol.Request) protocol.Event {
	d.mu.Lock()
	session, known := d.sessions[req.SessionID]
	d.mu.Unlock()
	if !known || session.Cwd == "" {
		return workspaceError(req, "that session is no longer available")
	}
	result := &protocol.WorkspaceResult{SessionID: req.SessionID, Path: req.Path}
	var err error
	switch req.Type {
	case protocol.ReqListCommits:
		result.Kind = "commits"
		result.Commits, result.Truncated, result.Branch, err = listCommits(ctx, session.Cwd, req.Ref, int(req.Offset))
	case protocol.ReqListBranches:
		result.Kind = "branches"
		result.Branches, err = listBranches(ctx, session.Cwd)
	case protocol.ReqCommitFiles:
		result.Kind = "commit"
		result.Commit, result.Changes, result.Truncated, result.Hidden, err = commitFiles(ctx, session.Cwd, req.Commit)
	case protocol.ReqCommitDiff:
		result.Kind = "diff"
		result.Diff, result.Truncated, err = commitDiff(ctx, session.Cwd, req.Commit, req.Path, req.IgnoreWhitespace)
		if err == nil {
			result.Text = commitContext(ctx, session.Cwd, req.Commit, req.Path)
		}
	}
	if err != nil {
		return workspaceError(req, err.Error())
	}
	return protocol.Event{Type: protocol.EvtWorkspace, Workspace: result}
}

// listCommits reads one page of the commits that touched cwd, from offset,
// on ref or, when ref is empty, on HEAD. more reports whether older commits
// follow.
//
// Reading another branch is git log, not a checkout: nothing is written, and
// an agent working in the directory keeps its files, its index and its branch.
func listCommits(ctx context.Context, cwd, ref string, offset int) (commits []protocol.WorkspaceCommit, more bool, branch string, err error) {
	prefix, _, err := runGit(ctx, cwd, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, false, "", err
	}
	// Detached, this fails and the branch is simply not named.
	if name, _, nameErr := runGit(ctx, cwd, "symbolic-ref", "--quiet", "--short", "HEAD"); nameErr == nil {
		branch = truncateUTF8(strings.TrimSpace(name), maxAuthorBytes, "")
	}
	commits = []protocol.WorkspaceCommit{}
	if ref != "" {
		// Checked here as well as when the request arrived: a branch can be
		// deleted between the listing and the tap.
		if _, _, refErr := runGit(ctx, cwd, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); refErr != nil {
			return nil, false, "", errors.New("that branch is no longer in this repository")
		}
	} else if _, _, headErr := runGit(ctx, cwd, "rev-parse", "--verify", "--quiet", "HEAD"); headErr != nil {
		// A repository with nothing committed yet has no history, which is
		// an answer rather than a failure.
		return commits, false, branch, nil
	}

	args := []string{"log", "--no-show-signature", "--format=" + recordSep + commitFields,
		"--skip=" + strconv.Itoa(offset), "-n", strconv.Itoa(commitPage + 1)}
	args = append(args, gitHistoryFlags...)
	if ref != "" {
		args = append(args, ref)
	}
	// A session in a subdirectory sees the commits that touched it, and its
	// counts cover only what changed there. At the top of a repository no
	// path is given at all: git log limited to "." prunes merges whose side
	// changed nothing, and this should read like plain git log. The "--"
	// keeps a branch from being mistaken for a file of the same name.
	paths := []string{"--"}
	if strings.TrimSpace(prefix) != "" {
		paths = append(paths, ".")
	}
	// Counted against each merge's first parent, which is what the merge
	// brought in, the way a pull request shows it. Counting costs a diff per
	// commit, so a repository where that takes too long still gets its
	// history, uncounted.
	text, truncated, err := runGit(ctx, cwd, append(append(args, "--shortstat", "--diff-merges=first-parent"), paths...)...)
	if err != nil {
		text, truncated, err = runGit(ctx, cwd, append(args, paths...)...)
		if err != nil {
			return nil, false, "", err
		}
	}
	records := strings.Split(text, recordSep)
	// The last record of a cut-off log may itself be cut off.
	if truncated && len(records) > 1 {
		records = records[:len(records)-1]
		more = true
	}
	for _, record := range records {
		header, stat, _ := strings.Cut(record, "\n")
		commit, ok := parseCommit(header)
		if !ok {
			continue
		}
		if match := shortStat.FindStringSubmatch(stat); match != nil {
			commit.Files, _ = strconv.Atoi(match[1])
			commit.Added, _ = strconv.Atoi(match[2])
			commit.Removed, _ = strconv.Atoi(match[3])
		}
		commits = append(commits, commit)
	}
	if len(commits) > commitPage {
		commits = commits[:commitPage]
		more = true
	}
	return commits, more, branch, nil
}

// maxBranches bounds the picker. A repository with more than this has old
// branches nobody is choosing from a phone, and newest come first.
const maxBranches = 200

// listBranches names the local and remote-tracking branches, newest first.
func listBranches(ctx context.Context, cwd string) ([]protocol.WorkspaceBranch, error) {
	text, _, err := runGit(ctx, cwd, "for-each-ref", "--sort=-committerdate",
		"--format=%(refname)%1f%(refname:short)%1f%(committerdate:unix)%1f%(HEAD)%1f%(symref)",
		"refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	branches := []protocol.WorkspaceBranch{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Split(line, fieldSep)
		// origin/HEAD only points at another branch, which is listed itself.
		if len(fields) != 5 || fields[4] != "" || !validBranchRef(fields[0]) {
			continue
		}
		seconds, _ := strconv.ParseInt(fields[2], 10, 64)
		branches = append(branches, protocol.WorkspaceBranch{
			Name:      truncateUTF8(fields[1], maxAuthorBytes, "…"),
			Ref:       fields[0],
			Remote:    strings.HasPrefix(fields[0], "refs/remotes/"),
			Current:   fields[3] == "*",
			UpdatedAt: seconds * 1000,
		})
		if len(branches) == maxBranches {
			break
		}
	}
	return branches, nil
}

// validBranchRef accepts a full local or remote-tracking branch name and
// nothing that Git would read as more than a name: no options, no revision
// syntax such as main^ or HEAD@{1}, no ranges, nothing outside refs/heads and
// refs/remotes. Starting with "refs/" is what keeps it from being an option.
func validBranchRef(ref string) bool {
	if len(ref) > 512 || !(strings.HasPrefix(ref, "refs/heads/") || strings.HasPrefix(ref, "refs/remotes/")) {
		return false
	}
	if strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.Contains(ref, "//") ||
		strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") || strings.HasSuffix(ref, ".lock") {
		return false
	}
	for _, r := range ref {
		if r <= ' ' || r == 0x7f || strings.ContainsRune("~^:?*[\\", r) {
			return false
		}
	}
	return true
}

// parseCommit reads the fields commitFields asked for. A record that does not
// lead with a hash is not one, which is also what becomes of a message that
// itself contains a separator.
func parseCommit(header string) (protocol.WorkspaceCommit, bool) {
	fields := strings.SplitN(header, fieldSep, 5)
	if len(fields) != 5 || !commitHash.MatchString(fields[0]) {
		return protocol.WorkspaceCommit{}, false
	}
	seconds, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return protocol.WorkspaceCommit{}, false
	}
	return protocol.WorkspaceCommit{
		Hash:       fields[0],
		Merge:      len(strings.Fields(fields[1])) > 1,
		Author:     truncateUTF8(fields[2], maxAuthorBytes, "…"),
		AuthoredAt: seconds * 1000,
		Subject:    truncateUTF8(fields[4], maxSubjectBytes, "…"),
	}, true
}

// commitBase is what a commit is compared with: its first parent, or Git's
// empty tree for a root commit, so the first commit shows as all additions.
// It also establishes that hash names a commit in this repository.
func commitBase(ctx context.Context, cwd, hash string) (string, error) {
	parents, _, err := runGit(ctx, cwd, "rev-list", "--parents", "-n", "1", hash)
	if err != nil {
		return "", errors.New("that commit is not in this repository")
	}
	fields := strings.Fields(parents)
	if len(fields) == 0 || fields[0] != hash {
		return "", errors.New("that commit is not in this repository")
	}
	if len(fields) > 1 {
		return fields[1], nil
	}
	emptyTree, _, err := runGit(ctx, cwd, "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(emptyTree), nil
}

// commitFiles describes one commit and lists the files it changed within cwd.
func commitFiles(ctx context.Context, cwd, hash string) (*protocol.WorkspaceCommit, []protocol.WorkspaceChange, bool, int, error) {
	base, err := commitBase(ctx, cwd, hash)
	if err != nil {
		return nil, nil, false, 0, err
	}
	text, _, err := runGit(ctx, cwd, "show", "-s", "--no-show-signature", "--no-color",
		"--format="+commitFields+fieldSep+"%b", hash)
	if err != nil {
		return nil, nil, false, 0, err
	}
	// The body is the sixth field and may itself hold a separator, so split
	// into exactly as many fields as were asked for.
	fields := strings.SplitN(text, fieldSep, 6)
	if len(fields) != 6 {
		return nil, nil, false, 0, errors.New("Git described this commit in a way agentman could not read")
	}
	commit, ok := parseCommit(strings.Join(fields[:5], fieldSep))
	if !ok {
		return nil, nil, false, 0, errors.New("Git described this commit in a way agentman could not read")
	}
	commit.Body = truncateUTF8(strings.TrimSpace(fields[5]), maxBodyBytes, "…")

	// --relative names each file from the session's directory and leaves out
	// whatever changed outside it. --raw carries each file's mode, which is
	// how a symlink is told apart from a file.
	raw, truncated, err := runGit(ctx, cwd, append([]string{"diff", "--raw", "-z", "--no-renames", "--relative", "--no-abbrev", base, hash}, gitHistoryFlags...)...)
	if err != nil {
		return nil, nil, false, 0, err
	}
	counts := map[string][2]int{}
	if numstat, _, err := runGit(ctx, cwd, append([]string{"diff", "--numstat", "-z", "--no-renames", "--relative", base, hash}, gitHistoryFlags...)...); err == nil {
		for _, item := range strings.Split(numstat, "\x00") {
			fields := strings.SplitN(item, "\t", 3)
			if len(fields) != 3 {
				continue
			}
			// "-" for a binary file, where a line count means nothing.
			added, addErr := strconv.Atoi(fields[0])
			removed, removeErr := strconv.Atoi(fields[1])
			if addErr == nil && removeErr == nil {
				counts[fields[2]] = [2]int{added, removed}
			}
		}
	}

	changes := []protocol.WorkspaceChange{}
	hidden := 0
	items := strings.Split(raw, "\x00")
	if truncated && len(items) > 0 {
		items = items[:len(items)-1]
	}
	for i := 0; i+1 < len(items); i += 2 {
		meta, name := items[i], items[i+1]
		// ":100644 100644 <old> <new> M"
		fields := strings.Fields(strings.TrimPrefix(meta, ":"))
		if len(fields) != 5 {
			continue
		}
		symlink := fields[0] == "120000" || fields[1] == "120000"
		if _, err := workspacePath(name, false); err != nil || symlink {
			hidden++
			continue
		}
		change := protocol.WorkspaceChange{Path: name, Status: fields[4][:1]}
		if count, ok := counts[name]; ok {
			change.Added, change.Removed = count[0], count[1]
		}
		changes = append(changes, change)
		commit.Files++
		commit.Added += change.Added
		commit.Removed += change.Removed
		if len(changes) >= maxDirectoryEntries {
			truncated = true
			break
		}
	}
	return &commit, changes, truncated, hidden, nil
}

// commitDiff is one file's change within one commit.
func commitDiff(ctx context.Context, cwd, hash, rel string, ignoreWhitespace bool) (string, bool, error) {
	base, err := commitBase(ctx, cwd, hash)
	if err != nil {
		return "", false, err
	}
	args := append([]string{"diff", "--relative", "--no-renames"}, gitHistoryFlags...)
	if ignoreWhitespace {
		args = append(args, "-w")
	}
	return runGit(ctx, cwd, append(args, base, hash, "--", rel)...)
}

// The new side of a historical diff, for expanding unchanged context. Read
// the exact blob rather than a working file, and never expose symlinks,
// submodules, binary data or incomplete text as context.
func commitContext(ctx context.Context, cwd, hash, rel string) string {
	tree, truncated, err := runGit(ctx, cwd, "ls-tree", "-z", hash, "--", rel)
	if err != nil || truncated {
		return ""
	}
	entries := strings.Split(strings.TrimSuffix(tree, "\x00"), "\x00")
	if len(entries) != 1 {
		return ""
	}
	meta, _, ok := strings.Cut(entries[0], "\t")
	fields := strings.Fields(meta)
	if !ok || len(fields) != 3 || (fields[0] != "100644" && fields[0] != "100755") || fields[1] != "blob" || !commitHash.MatchString(fields[2]) {
		return ""
	}
	text, truncated, err := runGit(ctx, cwd, "cat-file", "blob", fields[2])
	if err != nil || truncated || strings.ContainsRune(text, 0) || !utf8.ValidString(text) {
		return ""
	}
	return text
}
