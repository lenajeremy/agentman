package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Worktrees, so several agents can work in one repository at once, each in
// a folder of its own on a branch of its own. Agentman makes them under
// ~/Agentman/Worktrees/<repo>/<name>: in sight in Finder and editors, and
// where agents can be started (a launch folder can't be hidden). A fresh
// checkout lacks the untracked .env files a project runs with, so those
// are copied in. They are removed only when asked, and the branch stays.

const (
	worktreesFolder = "Agentman/Worktrees"
	maxWorktreeName = 80
	maxEnvFiles     = 40
	maxEnvFileSize  = 1 << 20
)

var worktreeSpaces = regexp.MustCompile(`\s+`)

func validateWorktree(req protocol.Request) error {
	if len(req.SessionID) > maxSessionIDBytes {
		return errors.New("daemon: invalid session id")
	}
	if req.Type == protocol.ReqCreateWorktree {
		if name := strings.TrimSpace(req.Text); name == "" || len(name) > maxWorktreeName {
			return errors.New("daemon: name the worktree, in at most 80 characters")
		}
	}
	if req.SessionID == "" || req.Type == protocol.ReqRemoveWorktree {
		return validateLaunchPath(req.Path, false)
	}
	return nil
}

func (d *Daemon) worktrees(ctx context.Context, req protocol.Request) protocol.Event {
	folder, err := d.worktreeFolder(req)
	if err != nil {
		return workspaceError(req, err.Error())
	}
	switch req.Type {
	case protocol.ReqCreateWorktree:
		worktree, err := createWorktree(ctx, folder, req.Text)
		if err != nil {
			return workspaceError(req, err.Error())
		}
		return protocol.Event{Type: protocol.EvtWorktree, SessionID: req.SessionID, Worktree: worktree}
	case protocol.ReqRemoveWorktree:
		left, err := d.removeWorktree(ctx, folder, req.Force)
		if err != nil {
			return workspaceError(req, err.Error())
		}
		return protocol.Event{Type: protocol.EvtWorktrees, SessionID: req.SessionID, Worktrees: left}
	default:
		list, err := listWorktrees(ctx, folder)
		if err != nil {
			return workspaceError(req, err.Error())
		}
		return protocol.Event{Type: protocol.EvtWorktrees, SessionID: req.SessionID, Worktrees: list}
	}
}

// worktreeFolder is the folder a request is about: a session's, or a path
// under home.
func (d *Daemon) worktreeFolder(req protocol.Request) (string, error) {
	if req.SessionID != "" && req.Type != protocol.ReqRemoveWorktree {
		d.mu.Lock()
		session, known := d.sessions[req.SessionID]
		d.mu.Unlock()
		if !known || session.Cwd == "" {
			return "", errors.New("that session is no longer available")
		}
		return session.Cwd, nil
	}
	folder, err := launchDirectory(req.Path, false)
	if err != nil {
		return "", errors.New(strings.TrimPrefix(err.Error(), "daemon: "))
	}
	return folder, nil
}

// createWorktree makes one named name from the repository folder is in, on
// a new branch of that name from what folder has checked out.
func createWorktree(ctx context.Context, folder, name string) (*protocol.Worktree, error) {
	branch := strings.Trim(worktreeSpaces.ReplaceAllString(strings.TrimSpace(name), "-"), "-/")
	if branch == "" {
		return nil, errors.New("name the worktree")
	}
	if _, _, err := runGit(ctx, folder, "check-ref-format", "--branch", branch); err != nil {
		return nil, fmt.Errorf("%q can't be a branch name: use letters, numbers, - and /", branch)
	}
	top, _, err := runGit(ctx, folder, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	top = strings.TrimSpace(top)
	if _, _, err := runGit(ctx, top, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		return nil, fmt.Errorf("a branch named %s already exists; pick another name", branch)
	}
	repo, err := repoName(ctx, top)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	target := filepath.Join(home, worktreesFolder, repo, strings.ReplaceAll(branch, "/", "-"))
	if _, err := os.Lstat(target); err == nil {
		return nil, fmt.Errorf("~/%s already exists; pick another name", filepath.ToSlash(filepath.Join(worktreesFolder, repo, filepath.Base(target))))
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}
	if _, err := runGitAction(ctx, top, "git", "worktree", "add", "-b", branch, target, "HEAD"); err != nil {
		return nil, err
	}
	return &protocol.Worktree{
		Path: target, Name: filepath.Base(target), Repo: repo, Branch: branch, Agentman: true,
		Copied: copyEnvFiles(ctx, top, target),
	}, nil
}

// repoName is the name of the repository's own folder, the same from any
// of its worktrees.
func repoName(ctx context.Context, folder string) (string, error) {
	common, _, err := runGit(ctx, folder, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	common = filepath.Clean(strings.TrimSpace(common))
	if filepath.Base(common) == ".git" {
		return filepath.Base(filepath.Dir(common)), nil
	}
	return strings.TrimSuffix(filepath.Base(common), ".git"), nil
}

// copyEnvFiles copies the .env files git doesn't track, a project's own
// settings and secrets, from one worktree to another, where they keep
// their places; one already there is left alone. It reports what it copied.
func copyEnvFiles(ctx context.Context, from, to string) []string {
	found := map[string]bool{}
	for _, args := range [][]string{
		{"ls-files", "--others", "--exclude-standard", "-z"},
		{"ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z"},
	} {
		listed, _, err := runGit(ctx, from, args...)
		if err != nil {
			continue
		}
		for _, rel := range strings.Split(listed, "\x00") {
			if name := path.Base(rel); !strings.HasSuffix(rel, "/") && (name == ".env" || strings.HasPrefix(name, ".env.")) {
				found[rel] = true
			}
		}
	}
	names := make([]string, 0, len(found))
	for rel := range found {
		names = append(names, rel)
	}
	sort.Strings(names)
	var copied []string
	for _, rel := range names {
		if len(copied) == maxEnvFiles {
			break
		}
		if copyFile(filepath.Join(from, rel), filepath.Join(to, filepath.FromSlash(rel))) == nil {
			copied = append(copied, rel)
		}
	}
	return copied
}

func copyFile(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxEnvFileSize {
		return errors.New("not a small regular file")
	}
	if _, err := os.Lstat(dst); err == nil {
		return errors.New("already there")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// listWorktrees lists a repository's worktrees, its own folder first.
func listWorktrees(ctx context.Context, folder string) ([]protocol.Worktree, error) {
	listed, _, err := runGit(ctx, folder, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	repo, err := repoName(ctx, folder)
	if err != nil {
		return nil, err
	}
	root := agentmanWorktrees()
	var list []protocol.Worktree
	for _, block := range strings.Split(strings.TrimSpace(listed), "\n\n") {
		var worktree protocol.Worktree
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				worktree.Path = strings.TrimPrefix(line, "worktree ")
			case strings.HasPrefix(line, "branch "):
				worktree.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
			}
		}
		if worktree.Path == "" {
			continue
		}
		worktree.Name = filepath.Base(worktree.Path)
		worktree.Repo = repo
		worktree.Main = len(list) == 0
		worktree.Agentman = root != "" && strings.HasPrefix(realPath(worktree.Path), root+string(filepath.Separator))
		list = append(list, worktree)
	}
	return list, nil
}

// removeWorktree removes a worktree Agentman made, keeping its branch. One
// with uncommitted changes, or an agent still working in it, is removed
// only when forced. It answers with the repository's worktrees left.
func (d *Daemon) removeWorktree(ctx context.Context, folder string, force bool) ([]protocol.Worktree, error) {
	root := agentmanWorktrees()
	target := realPath(folder)
	if root == "" || !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return nil, errors.New("only worktrees Agentman made, in ~/" + worktreesFolder + ", can be removed here")
	}
	list, err := listWorktrees(ctx, target)
	if err != nil {
		return nil, err
	}
	var main string
	known := false
	for _, worktree := range list {
		if worktree.Main {
			main = worktree.Path
		}
		known = known || realPath(worktree.Path) == target
	}
	if !known || main == "" {
		return nil, errors.New("that folder isn't one of its repository's worktrees")
	}
	if !force {
		for _, session := range d.snapshot() {
			cwd := realPath(session.Cwd)
			if session.State != protocol.StateEnded && (cwd == target || strings.HasPrefix(cwd, target+string(filepath.Separator))) {
				return nil, fmt.Errorf("%s is still working in it; end it first, or remove the worktree anyway", session.Name)
			}
		}
		if status, _, err := runGit(ctx, target, "status", "--porcelain"); err == nil && strings.TrimSpace(status) != "" {
			return nil, errors.New("it has uncommitted changes; commit them, or remove the worktree anyway to lose them")
		}
	}
	// Checked above, so git may take the ignored files too, the copied .env
	// files among them.
	if _, err := runGitAction(ctx, main, "git", "worktree", "remove", "--force", target); err != nil {
		return nil, err
	}
	return listWorktrees(ctx, main)
}

func agentmanWorktrees() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return realPath(filepath.Join(home, worktreesFolder))
}

// realPath is a path with its symlinks resolved, as far as it exists:
// /var and /private/var name one folder on a Mac.
func realPath(p string) string {
	if p == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(resolved, filepath.Base(p))
	}
	return filepath.Clean(p)
}
