package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lenajeremy/agentman/internal/protocol"
)

const (
	maxWorkspacePath    = 4096
	maxDirectoryEntries = 500
	maxTextFile         = 256 << 10
	maxImageFile        = 2 << 20
	maxGitOutput        = 256 << 10
)

// workspacePath accepts only relative paths beneath a session's cwd. Root.Open
// enforces the actual boundary, including symlinks; this also removes paths we
// never want to offer through a phone, even to a paired device.
func workspacePath(raw string, allowRoot bool) (string, error) {
	if raw == "" && allowRoot {
		return ".", nil
	}
	if len(raw) == 0 || len(raw) > maxWorkspacePath || strings.HasPrefix(raw, "/") ||
		strings.ContainsAny(raw, "\\\x00") {
		return "", errors.New("invalid workspace path")
	}
	parts := strings.Split(raw, "/")
	for _, part := range parts {
		if part == "" || part == ".." || part == "." || privatePart(part) {
			return "", errors.New("that path is not available in Agentman")
		}
	}
	return path.Join(parts...), nil
}

// privatePart reports whether one path segment must never leave the Mac.
//
// This used to be a blanket rule against anything beginning with a dot, with a
// short allowlist for the config files people wanted to see. It hid far more
// than it protected: .agents, .claude, .vscode, .config and every other dot
// folder an agent works in vanished — and the screen then reported no changes
// at all rather than admitting it had withheld some. A denylist of the names
// that genuinely carry credentials is both easier to reason about and honest
// about what it covers.
//
// Only a session's own directory is ever read, never a home directory, so the
// exposure this guards against is a credential committed inside a repo — a
// .env file, an .npmrc holding a token — and not ~/.ssh, which was never
// reachable from here.
func privatePart(part string) bool {
	lower := strings.ToLower(part)
	switch lower {
	// Version-control internals: noise, and .git/config can hold a token.
	case ".git", ".hg", ".svn",
		// Credential stores that occasionally appear inside a project.
		".ssh", ".gnupg", ".aws", ".azure", ".kube", ".docker",
		".npmrc", ".netrc", ".pypirc", ".pgpass", ".my.cnf",
		".git-credentials", ".terraformrc", ".htpasswd",
		"credentials", "credentials.json",
		// Agentman's own state, and a directory nobody reviews by hand.
		".agentman", "node_modules":
		return true
	}
	return strings.HasPrefix(lower, ".env") ||
		strings.HasPrefix(lower, "id_rsa") ||
		strings.HasPrefix(lower, "id_ed25519") ||
		strings.HasSuffix(lower, ".pem") ||
		strings.HasSuffix(lower, ".key") ||
		strings.HasSuffix(lower, ".p12") ||
		strings.HasSuffix(lower, ".pfx") ||
		strings.HasSuffix(lower, ".keystore") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "credential")
}

func (d *Daemon) workspace(ctx context.Context, req protocol.Request) protocol.Event {
	d.mu.Lock()
	session, known := d.sessions[req.SessionID]
	d.mu.Unlock()
	if !known || session.Cwd == "" {
		return workspaceError(req, "that session is no longer available")
	}
	allowRoot := req.Type == protocol.ReqListFiles || req.Type == protocol.ReqListChanges
	rel, err := workspacePath(req.Path, allowRoot)
	if err != nil {
		return workspaceError(req, err.Error())
	}
	root, err := os.OpenRoot(session.Cwd)
	if err != nil {
		return workspaceError(req, "the session directory is unavailable")
	}
	defer root.Close()
	if err := rejectWorkspaceSymlinks(root, rel, req.Type == protocol.ReqFileDiff); err != nil {
		return workspaceError(req, err.Error())
	}
	if req.Type == protocol.ReqFileDiff {
		info, statErr := root.Stat(rel)
		if statErr == nil && !info.Mode().IsRegular() {
			return workspaceError(req, "choose a file to view its diff")
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return workspaceError(req, statErr.Error())
		}
	}

	result := &protocol.WorkspaceResult{SessionID: req.SessionID, Path: req.Path}
	switch req.Type {
	case protocol.ReqListFiles:
		result.Kind = "directory"
		result.Entries, result.Truncated, result.Hidden, err = listWorkspace(root, rel)
	case protocol.ReqReadFile:
		result.Kind = "file"
		result.Text, result.Image, result.MIME, result.Truncated, err = readWorkspace(root, rel)
	case protocol.ReqListChanges:
		result.Kind = "changes"
		result.Changes, result.Truncated, result.Hidden, err = listChanges(ctx, session.Cwd, root, rel)
	case protocol.ReqFileDiff:
		result.Kind = "diff"
		result.Diff, result.Truncated, err = fileDiff(ctx, session.Cwd, root, rel)
	}
	if err != nil {
		return workspaceError(req, err.Error())
	}
	return protocol.Event{Type: protocol.EvtWorkspace, Workspace: result}
}

func rejectWorkspaceSymlinks(root *os.Root, rel string, allowMissing bool) error {
	if rel == "." {
		return nil
	}
	parts := strings.Split(rel, "/")
	for i := range parts {
		info, err := root.Lstat(path.Join(parts[:i+1]...))
		if err != nil {
			// A deleted file may have taken its parent directories with it.
			if allowMissing && errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink paths are unavailable")
		}
	}
	return nil
}

func workspaceError(req protocol.Request, message string) protocol.Event {
	return protocol.Event{Type: protocol.EvtError, SessionID: req.SessionID, Error: message}
}

func listWorkspace(root *os.Root, rel string) ([]protocol.WorkspaceEntry, bool, int, error) {
	f, err := root.Open(rel)
	if err != nil {
		return nil, false, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false, 0, err
	}
	if !info.IsDir() {
		return nil, false, 0, errors.New("this path is not a folder")
	}
	entries := make([]protocol.WorkspaceEntry, 0, maxDirectoryEntries)
	truncated := false
	hidden := 0
	for scanned := 0; scanned < 5000; {
		items, readErr := f.ReadDir(128)
		if readErr != nil && readErr != io.EOF {
			return nil, false, 0, readErr
		}
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			scanned++
			if privatePart(item.Name()) || item.Type()&os.ModeSymlink != 0 {
				hidden++
				continue
			}
			info, err := item.Info()
			if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
				continue
			}
			entries = append(entries, protocol.WorkspaceEntry{Name: item.Name(), Directory: info.IsDir(), Size: info.Size()})
			if len(entries) == maxDirectoryEntries {
				truncated = true
				break
			}
		}
		if truncated || scanned >= 5000 {
			truncated = true
			break
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Directory != entries[j].Directory {
			return entries[i].Directory
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, truncated, hidden, nil
}

func readWorkspace(root *os.Root, rel string) (text, image, mime string, truncated bool, err error) {
	f, err := root.Open(rel)
	if err != nil {
		return "", "", "", false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", "", "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", "", "", false, errors.New("only regular files can be read")
	}
	var head [512]byte
	n, _ := f.Read(head[:])
	mime = http.DetectContentType(head[:n])
	_, _ = f.Seek(0, io.SeekStart)
	if mime == "image/png" || mime == "image/jpeg" || mime == "image/gif" || mime == "image/webp" {
		if info.Size() > maxImageFile {
			return "", "", "", false, errors.New("image is too large to preview (2 MiB limit)")
		}
		data, readErr := io.ReadAll(io.LimitReader(f, maxImageFile+1))
		if readErr != nil {
			return "", "", "", false, readErr
		}
		if len(data) > maxImageFile {
			return "", "", "", false, errors.New("image is too large to preview (2 MiB limit)")
		}
		return "", base64.StdEncoding.EncodeToString(data), mime, false, nil
	}
	if bytes.IndexByte(head[:n], 0) >= 0 {
		return "", "", "", false, errors.New("binary file preview is unavailable")
	}
	data, readErr := io.ReadAll(io.LimitReader(f, maxTextFile+1))
	if readErr != nil {
		return "", "", "", false, readErr
	}
	truncated = len(data) > maxTextFile
	if truncated {
		data = data[:maxTextFile]
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
	}
	if !utf8.Valid(data) {
		return "", "", "", false, errors.New("this file is not UTF-8 text")
	}
	return string(data), "", "text/plain", truncated, nil
}

type boundedOutput struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		keep := max(0, b.limit-b.Len())
		_, _ = b.Buffer.Write(p[:keep])
		b.truncated = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

func runGit(ctx context.Context, cwd string, args ...string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--literal-pathspecs", "--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-C", cwd}, args...)...)
	var out boundedOutput
	out.limit = maxGitOutput
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", false, errors.New("Git took too long")
		}
		if strings.Contains(stderr.String(), "not a git repository") {
			return "", false, errors.New("this directory is not a Git working tree")
		}
		return "", false, errors.New("Git is unavailable for this directory")
	}
	return out.String(), out.truncated, nil
}

func listChanges(ctx context.Context, cwd string, root *os.Root, rel string) ([]protocol.WorkspaceChange, bool, int, error) {
	prefix, _, err := runGit(ctx, cwd, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, false, 0, err
	}
	text, truncated, err := runGit(ctx, cwd, "status", "--porcelain=v1", "--no-renames", "-z", "--untracked-files=all", "--", rel)
	if err != nil {
		return nil, false, 0, err
	}
	changes := []protocol.WorkspaceChange{}
	hidden := 0
	if truncated {
		text = text[:strings.LastIndex(text, "\x00")+1]
	}
	for _, item := range strings.Split(text, "\x00") {
		if len(item) < 4 {
			continue
		}
		rootName := item[3:]
		prefix = strings.TrimSpace(prefix)
		if prefix != "" && !strings.HasPrefix(rootName, prefix) {
			continue
		}
		name := strings.TrimPrefix(rootName, prefix)
		if filepath.IsAbs(name) {
			continue
		}
		if _, err := workspacePath(name, false); err != nil {
			hidden++
			continue
		}
		if err := rejectWorkspaceSymlinks(root, name, true); err != nil {
			hidden++
			continue
		}
		changes = append(changes, protocol.WorkspaceChange{Path: name, Status: item[:2]})
		if len(changes) >= maxDirectoryEntries {
			truncated = true
			break
		}
	}
	addLineCounts(ctx, cwd, root, rel, changes)
	return changes, truncated, hidden, nil
}

// addLineCounts fills in each change's added/removed lines.
//
// Two sources, because git has no single one: `diff --numstat` covers tracked
// files but says nothing about untracked ones, which are exactly the files a
// reviewer most wants sized. An untracked file is entirely new, so every line
// in it counts as added, read back through the same Root that guards every
// other read here.
func addLineCounts(ctx context.Context, cwd string, root *os.Root, rel string, changes []protocol.WorkspaceChange) {
	if len(changes) == 0 {
		return
	}
	// One call for every tracked file rather than one per file: a status with
	// hundreds of entries would otherwise mean hundreds of git processes.
	// HEAD may not exist yet in a fresh repository, where everything is
	// untracked anyway and the fallback below covers it.
	byPath := map[string]protocol.WorkspaceChange{}
	if text, _, err := runGit(ctx, cwd, "diff", "--numstat", "--no-renames", "HEAD", "--", rel); err == nil {
		for _, line := range strings.Split(text, "\n") {
			fields := strings.Split(strings.TrimSpace(line), "\t")
			if len(fields) != 3 {
				continue
			}
			// git writes "-" for a binary file, where a line count is
			// meaningless rather than zero.
			added, addErr := strconv.Atoi(fields[0])
			removed, removeErr := strconv.Atoi(fields[1])
			if addErr != nil || removeErr != nil {
				continue
			}
			byPath[fields[2]] = protocol.WorkspaceChange{Added: added, Removed: removed}
		}
	}

	prefix, _, _ := runGit(ctx, cwd, "rev-parse", "--show-prefix")
	prefix = strings.TrimSpace(prefix)
	for i := range changes {
		if counts, ok := byPath[prefix+changes[i].Path]; ok {
			changes[i].Added = counts.Added
			changes[i].Removed = counts.Removed
			continue
		}
		if strings.HasPrefix(changes[i].Status, "??") {
			changes[i].Added = countLines(root, changes[i].Path)
		}
	}
}

// countLines reports the lines in an untracked file, bounded by the same
// ceiling as a text preview so a huge or binary file cannot be read whole
// just to put a number beside its name.
func countLines(root *os.Root, rel string) int {
	f, err := root.Open(rel)
	if err != nil {
		return 0
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxTextFile))
	if err != nil || !utf8.Valid(data) {
		return 0
	}
	if len(data) == 0 {
		return 0
	}
	lines := bytes.Count(data, []byte("\n"))
	if !bytes.HasSuffix(data, []byte("\n")) {
		lines++
	}
	return lines
}

func fileDiff(ctx context.Context, cwd string, root *os.Root, rel string) (string, bool, error) {
	status, _, err := runGit(ctx, cwd, "status", "--porcelain=v1", "--no-renames", "--", rel)
	if err != nil {
		return "", false, err
	}
	if strings.HasPrefix(status, "?? ") {
		text, image, _, truncated, err := readWorkspace(root, rel)
		if err != nil {
			return "", false, err
		}
		if image != "" {
			return "Binary image (new file)", false, nil
		}
		var diff strings.Builder
		diff.WriteString("--- /dev/null\n+++ b/" + rel + "\n")
		for _, line := range strings.SplitAfter(text, "\n") {
			if line != "" {
				if diff.Len()+len(line)+1 > maxGitOutput {
					truncated = true
					break
				}
				diff.WriteString("+" + line)
			}
		}
		return diff.String(), truncated, nil
	}
	base := "HEAD"
	if _, _, err := runGit(ctx, cwd, "rev-parse", "--verify", "HEAD"); err != nil {
		// Before the first commit, compare against Git's empty tree. This also
		// includes edits made after a file was staged.
		emptyTree, _, hashErr := runGit(ctx, cwd, "hash-object", "-t", "tree", "--stdin")
		if hashErr != nil {
			return "", false, hashErr
		}
		base = strings.TrimSpace(emptyTree)
	}
	text, truncated, err := runGit(ctx, cwd, "diff", "--no-ext-diff", "--no-textconv", base, "--", rel)
	if err != nil {
		return "", false, err
	}
	return text, truncated, nil
}
