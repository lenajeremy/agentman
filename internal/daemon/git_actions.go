package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// The right panel's git actions, as T3 Code has them: where the branch
// stands, then Commit, Push and Create PR, run in the agent's folder as the
// user would run them there, their own hooks and credentials included.

const (
	// gitActionTimeout bounds a commit's hooks, a push over a slow link, or
	// the GitHub CLI.
	gitActionTimeout = 2 * time.Minute
	// maxCommitPaths bounds the files one commit names.
	maxCommitPaths = 5000
	// maxCommitDiff is how much of a change Agentman reads to write its
	// commit message.
	maxCommitDiff = 24 << 10
)

// CommitWriter is a manager that can write a commit message for a change:
// Agentman, given its key. Separate from ManagerHook so a manager without it
// still serves.
type CommitWriter interface {
	CommitMessage(ctx context.Context, diff string) (string, error)
}

func validateCommit(message string, paths []string) error {
	if len(message) > maxMessageBytes || strings.ContainsRune(message, 0) {
		return fmt.Errorf("daemon: that commit message is too long")
	}
	if len(paths) == 0 {
		return fmt.Errorf("daemon: choose the files to commit")
	}
	if len(paths) > maxCommitPaths {
		return fmt.Errorf("daemon: too many files for one commit")
	}
	for _, path := range paths {
		if _, err := workspacePath(path, false); err != nil {
			return err
		}
	}
	return nil
}

func (d *Daemon) gitAction(ctx context.Context, req protocol.Request) protocol.Event {
	d.mu.Lock()
	session, known := d.sessions[req.SessionID]
	d.mu.Unlock()
	if !known || session.Cwd == "" {
		return workspaceError(req, "that session is no longer available")
	}
	cwd := session.Cwd

	var done, commit, message, url string
	var err error
	switch req.Type {
	case protocol.ReqGitCommit:
		done = "committed"
		commit, message, err = d.gitCommit(ctx, cwd, req.Text, req.Paths)
	case protocol.ReqGitPush:
		done = "pushed"
		err = gitPush(ctx, cwd)
	case protocol.ReqGitCreatePR:
		done = "pr_created"
		url, err = gitCreatePR(ctx, cwd)
	}
	if err != nil {
		return workspaceError(req, err.Error())
	}
	state, err := gitState(ctx, cwd)
	if err != nil {
		return workspaceError(req, err.Error())
	}
	state.SessionID = req.SessionID
	state.Done, state.Commit, state.Message, state.URL = done, commit, message, url
	return protocol.Event{Type: protocol.EvtGit, SessionID: req.SessionID, Git: state}
}

// gitState reads where the branch stands. Read-only, through runGit, so no
// hook runs and nothing is fetched.
func gitState(ctx context.Context, cwd string) (*protocol.GitState, error) {
	state := &protocol.GitState{}
	head, _, err := runGit(ctx, cwd, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil {
		state.Branch = strings.TrimSpace(head)
	} else if _, _, err := runGit(ctx, cwd, "rev-parse", "--git-dir"); err != nil {
		return nil, err
	}

	if remotes, _, err := runGit(ctx, cwd, "remote"); err == nil {
		for _, name := range strings.Fields(remotes) {
			if state.Remote == "" || name == "origin" {
				state.Remote = name
			}
		}
	}
	if state.Remote != "" {
		if ref, _, err := runGit(ctx, cwd, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+state.Remote+"/HEAD"); err == nil {
			state.DefaultBranch = strings.TrimPrefix(strings.TrimSpace(ref), state.Remote+"/")
		}
	}
	if state.DefaultBranch == "" {
		for _, name := range []string{"main", "master"} {
			if _, _, err := runGit(ctx, cwd, "rev-parse", "--verify", "--quiet", "refs/heads/"+name); err == nil {
				state.DefaultBranch = name
				break
			}
		}
	}

	if state.Branch != "" {
		if upstream, _, err := runGit(ctx, cwd, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
			state.Upstream = strings.TrimSpace(upstream)
			if counts, _, err := runGit(ctx, cwd, "rev-list", "--left-right", "--count", "@{upstream}...HEAD"); err == nil {
				_, _ = fmt.Sscan(counts, &state.Behind, &state.Ahead)
			}
		} else if state.Remote != "" {
			// Never pushed: what no remote branch has yet.
			if count, _, err := runGit(ctx, cwd, "rev-list", "--count", "HEAD", "--not", "--remotes"); err == nil {
				_, _ = fmt.Sscan(count, &state.Ahead)
			}
		}
		if state.DefaultBranch != "" && state.Branch != state.DefaultBranch {
			base := state.DefaultBranch
			if state.Remote != "" {
				if _, _, err := runGit(ctx, cwd, "rev-parse", "--verify", "--quiet", "refs/remotes/"+state.Remote+"/"+base); err == nil {
					base = state.Remote + "/" + base
				}
			}
			if count, _, err := runGit(ctx, cwd, "rev-list", "--count", base+"..HEAD"); err == nil {
				_, _ = fmt.Sscan(count, &state.AheadOfDefault)
			}
		}
	}

	if status, _, err := runGit(ctx, cwd, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", "."); err == nil {
		for _, item := range strings.Split(status, "\x00") {
			if len(item) > 3 {
				state.Changes++
			}
		}
	}
	return state, nil
}

// gitCommit commits the files named, and only those: other changes, staged
// or not, stay as they were. With no message, Agentman writes one.
func (d *Daemon) gitCommit(ctx context.Context, cwd, message string, paths []string) (string, string, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		written, err := d.writeCommitMessage(ctx, cwd, paths)
		if err != nil {
			return "", "", err
		}
		message = written
	}
	if _, err := runGitAction(ctx, cwd, "git", append([]string{"add", "--all", "--"}, paths...)...); err != nil {
		return "", "", err
	}
	if _, err := runGitAction(ctx, cwd, "git", append([]string{"commit", "--message", message, "--only", "--"}, paths...)...); err != nil {
		return "", "", err
	}
	hash, _, _ := runGit(ctx, cwd, "rev-parse", "--short", "HEAD")
	return strings.TrimSpace(hash), message, nil
}

// writeCommitMessage asks Agentman for a message for the change to the
// files named, from their diff against HEAD, read without staging anything.
func (d *Daemon) writeCommitMessage(ctx context.Context, cwd string, paths []string) (string, error) {
	writer, ok := d.managerHook().(CommitWriter)
	if !ok {
		return "", errors.New("write a commit message: Agentman isn't running to write one")
	}
	var diff strings.Builder
	if stat, _, err := runGit(ctx, cwd, append([]string{"diff", "--no-ext-diff", "--stat", "HEAD", "--"}, paths...)...); err == nil {
		diff.WriteString(stat)
	}
	if status, _, err := runGit(ctx, cwd, append([]string{"status", "--porcelain=v1", "--untracked-files=all", "--"}, paths...)...); err == nil {
		for _, line := range strings.Split(status, "\n") {
			if strings.HasPrefix(line, "?? ") {
				fmt.Fprintf(&diff, "new file: %s\n", line[3:])
			}
		}
	}
	if text, _, err := runGit(ctx, cwd, append([]string{"diff", "--no-ext-diff", "--no-textconv", "HEAD", "--"}, paths...)...); err == nil {
		diff.WriteString("\n")
		diff.WriteString(text)
	}
	change := diff.String()
	if len(change) > maxCommitDiff {
		change = change[:maxCommitDiff]
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	message, err := writer.CommitMessage(ctx, change)
	if err != nil {
		return "", fmt.Errorf("write a commit message: %w", err)
	}
	return message, nil
}

// gitPush pushes the branch to its upstream, or, the first time, to the
// remote under its own name, made its upstream.
func gitPush(ctx context.Context, cwd string) error {
	state, err := gitState(ctx, cwd)
	if err != nil {
		return err
	}
	if state.Branch == "" {
		return errors.New("check out a branch to push")
	}
	if state.Upstream != "" {
		_, err = runGitAction(ctx, cwd, "git", "push")
		return err
	}
	if state.Remote == "" {
		return errors.New("this repository has no remote to push to")
	}
	_, err = runGitAction(ctx, cwd, "git", "push", "--set-upstream", state.Remote, "HEAD:refs/heads/"+state.Branch)
	return err
}

// gitCreatePR pushes what isn't pushed, then opens a pull request from the
// branch with the GitHub CLI, titled and described from its commits. One
// already open is answered with its link.
func gitCreatePR(ctx context.Context, cwd string) (string, error) {
	gh, err := exec.LookPath("gh")
	if err != nil {
		return "", errors.New("install the GitHub CLI (gh) and sign in with `gh auth login` to create pull requests")
	}
	state, err := gitState(ctx, cwd)
	if err != nil {
		return "", err
	}
	if state.Branch == "" {
		return "", errors.New("check out a branch to open a pull request from")
	}
	if state.Branch == state.DefaultBranch {
		return "", fmt.Errorf("a pull request can't come from %s, the default branch: commit on a new branch", state.Branch)
	}
	if state.Upstream == "" || state.Ahead > 0 {
		if err := gitPush(ctx, cwd); err != nil {
			return "", err
		}
	}
	args := []string{"pr", "create", "--fill", "--head", state.Branch}
	if state.DefaultBranch != "" {
		args = append(args, "--base", state.DefaultBranch)
	}
	out, err := runGitAction(ctx, cwd, gh, args...)
	if err != nil {
		// "a pull request for branch … already exists: https://…"
		if link := lastLink(err.Error()); link != "" && strings.Contains(err.Error(), "already exists") {
			return link, nil
		}
		return "", err
	}
	if link := lastLink(out); link != "" {
		return link, nil
	}
	return "", errors.New("the pull request was made, but gh didn't say where")
}

func lastLink(text string) string {
	fields := strings.Fields(text)
	for i := len(fields) - 1; i >= 0; i-- {
		if strings.HasPrefix(fields[i], "https://") {
			return fields[i]
		}
	}
	return ""
}

// runGitAction runs git, or gh, as the user would: with their hooks and
// credential helpers, and with nothing that waits for input that can't
// come. In a session of its own, ssh has no terminal to ask a passphrase
// on and fails at once instead; git doesn't prompt for a password either.
func runGitAction(ctx context.Context, cwd, program string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitActionTimeout)
	defer cancel()
	if program == "git" {
		args = append([]string{"--literal-pathspecs", "-C", cwd}, args...)
	}
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var stdout, stderr boundedOutput
	stdout.limit, stderr.limit = maxGitOutput, 64<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s took too long", filepath.Base(program))
		}
		return "", actionFailure(stderr.String(), stdout.String())
	}
	return stdout.String(), nil
}

// actionFailure is what git or gh said went wrong: its last few lines,
// without hints or "error:"/"fatal:" prefixes.
func actionFailure(stderr, stdout string) error {
	text := strings.TrimSpace(stderr)
	if text == "" {
		text = strings.TrimSpace(stdout)
	}
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "hint:") {
			continue
		}
		line = strings.TrimPrefix(strings.TrimPrefix(line, "error: "), "fatal: ")
		lines = append(lines, line)
	}
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	if len(lines) == 0 {
		return errors.New("git failed")
	}
	return errors.New(strings.Join(lines, "\n"))
}
