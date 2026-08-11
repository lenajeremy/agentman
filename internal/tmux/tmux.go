// Package tmux launches agent CLIs inside tmux and types into them.
//
// Agent CLIs are interactive terminal programs with no input API: once one is
// running, there is no supported way to hand it another prompt. tmux solves
// that by owning the terminal — a session started through here can be typed
// into at any time, exactly as if the user had done it, including while the
// agent is mid-turn.
//
// This is the only delivery path that can interrupt a working agent. The
// alternative (see the hook queue) can only deliver between turns and is
// explicitly best-effort, which is why sessions started through this wrapper
// are the good case and the UI says so.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Prefix marks the tmux sessions we own, so agentman never types into a tmux
// session the user created for something else.
const Prefix = "agentman-"

// ErrNotInstalled is returned when tmux is unavailable.
var ErrNotInstalled = errors.New("tmux: not installed")

// commandTimeout bounds every tmux invocation. These are local and fast; a
// hang means something is wrong and waiting will not fix it.
const commandTimeout = 5 * time.Second

// Available reports whether tmux can be used.
func Available() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// Session is one tmux session running an agent.
type Session struct {
	Name string
	// PanePID is the process tmux started. The agent process is this or one of
	// its descendants, which is how a discovered agent session is matched back
	// to the tmux session that can receive its messages.
	PanePID int
	Cwd     string
	// Command is the program running in the pane ("codex", "claude"), which
	// is the only way to know an agent is there before it has written
	// anything to disk.
	Command string
	// Created is when tmux started the session. A stable value matters: a
	// session discovered from a pane has no file to take a timestamp from, and
	// using the current time instead makes it look changed on every sweep.
	Created time.Time
}

// List returns the agentman-owned tmux sessions currently running.
func List(ctx context.Context) ([]Session, error) {
	if !Available() {
		return nil, ErrNotInstalled
	}
	out, err := run(ctx, "list-sessions", "-F",
		"#{session_name}\t#{pane_pid}\t#{pane_current_path}\t#{pane_current_command}\t#{session_created}")
	if err != nil {
		// tmux exits non-zero when no server is running, which is normal.
		return nil, nil
	}

	var sessions []Session
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 || !strings.HasPrefix(fields[0], Prefix) {
			continue
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		session := Session{Name: fields[0], PanePID: pid, Cwd: fields[2]}
		if len(fields) > 3 {
			session.Command = fields[3]
		}
		if len(fields) > 4 {
			if unix, err := strconv.ParseInt(strings.TrimSpace(fields[4]), 10, 64); err == nil {
				session.Created = time.Unix(unix, 0)
			}
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

// Launch starts a command in a new detached tmux session and returns its name.
//
// Detached so the caller can decide whether to attach: `am claude` attaches
// immediately, giving the user their normal terminal experience while the
// session stays reachable from the phone.
func Launch(ctx context.Context, name, dir string, command []string) error {
	if !Available() {
		return ErrNotInstalled
	}
	if len(command) == 0 {
		return errors.New("tmux: no command given")
	}

	args := []string{"new-session", "-d", "-s", name}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	// The command is passed as separate argv entries so tmux executes it
	// directly rather than through a shell — no quoting, no interpretation of
	// anything in the user's arguments.
	args = append(args, command...)

	if _, err := run(ctx, args...); err != nil {
		return fmt.Errorf("tmux: could not start session: %w", err)
	}
	return nil
}

// Attach replaces the current process with a tmux client attached to name.
//
// Exec rather than a subprocess: the user should get tmux's terminal handling
// directly, with no wrapper sitting in between mangling signals or resizes.
func Attach(name string) error {
	binary, err := exec.LookPath("tmux")
	if err != nil {
		return ErrNotInstalled
	}
	args := []string{"tmux", "attach-session", "-t", name}
	return syscallExec(binary, args, os.Environ())
}

// Send types text into a session and submits it.
//
// Multi-line text is delivered through a paste buffer in bracketed-paste mode:
// sending raw newlines would submit the prompt at the first line break and
// scatter the rest across follow-up turns. Bracketed paste is how a terminal
// signals "this is pasted content", which is exactly what this is.
func Send(ctx context.Context, name, text string) error {
	if !Available() {
		return ErrNotInstalled
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("tmux: refusing to send an empty message")
	}

	// Clear whatever is already in the prompt box first.
	//
	// Typing into a box that holds a half-written draft fuses the two into one
	// garbled prompt ("this is false" + "run the tests" arrives as
	// "this is falserun the tests"), which is both wrong and unrecoverable
	// once submitted. Ctrl-U discards the line into the kill ring, so the
	// user's draft can still be restored with Ctrl-Y — a strictly better
	// outcome than sending nonsense to the agent.
	if _, err := run(ctx, "send-keys", "-t", name, "C-u"); err != nil {
		return fmt.Errorf("tmux: could not clear the prompt: %w", err)
	}

	if strings.ContainsAny(text, "\n\r") {
		if err := pasteMultiline(ctx, name, text); err != nil {
			return err
		}
	} else {
		// -l sends the text literally, so nothing in it is interpreted as a
		// key name: a message containing the word "Enter" stays that word.
		if _, err := run(ctx, "send-keys", "-t", name, "-l", text); err != nil {
			return fmt.Errorf("tmux: could not type into session: %w", err)
		}
	}

	// Submit as a separate key event. Some TUIs need a moment to process a
	// paste before the newline is treated as submission rather than content.
	time.Sleep(60 * time.Millisecond)
	if _, err := run(ctx, "send-keys", "-t", name, "Enter"); err != nil {
		return fmt.Errorf("tmux: could not submit: %w", err)
	}
	return nil
}

// pasteMultiline loads text into a private buffer and pastes it.
func pasteMultiline(ctx context.Context, name, text string) error {
	file, err := os.CreateTemp("", "agentman-paste-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())

	if _, err := file.WriteString(text); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}

	// A named buffer avoids disturbing whatever the user has in tmux's default
	// paste buffer.
	buffer := "agentman"
	if _, err := run(ctx, "load-buffer", "-b", buffer, file.Name()); err != nil {
		return fmt.Errorf("tmux: could not stage the message: %w", err)
	}
	// -p uses bracketed paste; -d deletes the buffer afterwards.
	if _, err := run(ctx, "paste-buffer", "-d", "-p", "-b", buffer, "-t", name); err != nil {
		return fmt.Errorf("tmux: could not paste the message: %w", err)
	}
	return nil
}

// Interrupt sends Ctrl-C, the terminal equivalent of stopping the agent.
func Interrupt(ctx context.Context, name string) error {
	if !Available() {
		return ErrNotInstalled
	}
	if _, err := run(ctx, "send-keys", "-t", name, "C-c"); err != nil {
		return fmt.Errorf("tmux: could not interrupt: %w", err)
	}
	return nil
}

// Kill terminates a session.
func Kill(ctx context.Context, name string) error {
	_, err := run(ctx, "kill-session", "-t", name)
	return err
}

// OwnsPID reports whether pid is the pane process or one of its descendants.
//
// Walking up from the agent's pid is how a session discovered on disk is
// matched to the tmux session that can type into it. Ancestry is used rather
// than the working directory because two agents can easily run in the same
// directory, and typing into the wrong one would be worse than not delivering.
func OwnsPID(panePID, pid int) bool {
	const maxDepth = 12 // guards against a cycle in a malformed process table
	for range maxDepth {
		if pid <= 1 {
			return false
		}
		if pid == panePID {
			return true
		}
		parent, ok := parentPID(pid)
		if !ok {
			return false
		}
		pid = parent
	}
	return false
}

func parentPID(pid int) (int, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, false
	}
	parent, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, false
	}
	return parent, true
}

func run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", args...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("%s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

// NewName mints a session name for an agent launch.
func NewName(kind string) string {
	return fmt.Sprintf("%s%s-%d", Prefix, kind, time.Now().Unix()%100000)
}

// Capture returns the visible contents of a session's pane.
//
// This is how a pending approval prompt is found: the CLIs fire no hook for
// one, and their session files still say "idle" while they sit blocked, so
// the terminal is the only place the truth exists.
func Capture(ctx context.Context, name string) (string, error) {
	if !Available() {
		return "", ErrNotInstalled
	}
	// -p prints to stdout; without -S the capture is the visible pane only,
	// which is exactly the region a prompt occupies.
	out, err := run(ctx, "capture-pane", "-t", name, "-p")
	if err != nil {
		return "", err
	}
	return out, nil
}

// Answer chooses an option in a menu the agent is showing.
//
// Deliberately not Send: a menu takes a single keystroke, and Send's
// prompt-clearing and trailing Enter would both be wrong here — Ctrl-U in a
// menu does nothing useful, and an extra Enter would confirm whatever the
// menu moved to next.
func Answer(ctx context.Context, name, key string) error {
	if !Available() {
		return ErrNotInstalled
	}
	if key == "" {
		return errors.New("tmux: no option given")
	}
	if _, err := run(ctx, "send-keys", "-t", name, "-l", key); err != nil {
		return fmt.Errorf("tmux: could not answer: %w", err)
	}
	return nil
}
