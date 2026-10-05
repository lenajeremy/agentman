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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/question"
)

// Prefix marks the tmux sessions we own, so agentman never types into a tmux
// session the user created for something else.
const Prefix = "agentman-"

// ErrNotInstalled is returned when tmux is unavailable.
var ErrNotInstalled = errors.New("tmux: not installed")

// commandTimeout bounds every tmux invocation. These are local and fast; a
// hang means something is wrong and waiting will not fix it.
const commandTimeout = 5 * time.Second

var (
	liveFormFooter     = regexp.MustCompile(`(?im)^\s*Enter to select\b`)
	focusedFormControl = regexp.MustCompile(`(?im)^\s*[❯›>»▸▶→*]\s*(?:Next|Submit)\s*$`)
)

// Agent actions are multi-command terminal transactions. Serialize actions
// aimed at the same pane so two phones cannot interleave clear/type/submit and
// accidentally fuse two instructions or answer a menu while a send is midway.
var actionLocks [64]sync.Mutex

func actionLock(name string) *sync.Mutex {
	var hash uint64 = 14695981039346656037
	for i := 0; i < len(name); i++ {
		hash ^= uint64(name[i])
		hash *= 1099511628211
	}
	return &actionLocks[hash%uint64(len(actionLocks))]
}

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

// List returns the agentman-owned tmux sessions currently running. Within a
// discovery sweep it is asked once; see WithSweep.
func List(ctx context.Context) ([]Session, error) {
	if s := sweepOf(ctx); s != nil {
		return s.list()
	}
	return listSessions(ctx)
}

func listSessions(ctx context.Context) ([]Session, error) {
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
	args := append([]string{"tmux"}, socketArgs()...)
	args = append(args, "attach-session", "-t", name)
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
	lock := actionLock(name)
	lock.Lock()
	defer lock.Unlock()

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
		// "--" ends the flags, so a message beginning with a dash is typed
		// rather than read by tmux as an option of its own.
		if _, err := run(ctx, "send-keys", "-t", name, "-l", "--", text); err != nil {
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
	// paste buffer. It must also be unique: two phone requests can paste at the
	// same time, and sharing one name lets one prompt overwrite the other's
	// staged contents before either paste completes.
	buffer := "agentman-" + uniqueSuffix()
	if _, err := run(ctx, "load-buffer", "-b", buffer, file.Name()); err != nil {
		return fmt.Errorf("tmux: could not stage the message: %w", err)
	}
	// paste-buffer -d normally removes it, but a missing pane or cancelled
	// paste fails before -d takes effect. Always attempt cleanup with a fresh
	// bounded context so sensitive prompt text is not left in tmux memory.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = run(cleanupCtx, "delete-buffer", "-b", buffer)
	}()
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
	lock := actionLock(name)
	lock.Lock()
	defer lock.Unlock()
	if _, err := run(ctx, "send-keys", "-t", name, "C-c"); err != nil {
		return fmt.Errorf("tmux: could not interrupt: %w", err)
	}
	return nil
}

// Escape sends one Escape key, which is how Kiro CLI and Antigravity CLI stop a
// running turn. Both advertise it in their own footers ("esc to cancel"), and
// Interrupt's Ctrl-C is a different request in their interfaces, not a
// synonym for it.
func Escape(ctx context.Context, name string) error {
	if !Available() {
		return ErrNotInstalled
	}
	lock := actionLock(name)
	lock.Lock()
	defer lock.Unlock()
	if _, err := run(ctx, "send-keys", "-t", name, "Escape"); err != nil {
		return fmt.Errorf("tmux: could not interrupt: %w", err)
	}
	return nil
}

// Kill terminates a session.
func Kill(ctx context.Context, name string) error {
	_, err := run(ctx, "kill-session", "-t", name)
	return err
}

// ProcessTree is one immutable snapshot of the operating system's PID → parent
// relationships. A discovery sweep can perform any number of ancestry checks
// against it without spawning another process or observing an inconsistent
// process table halfway through the sweep.
type ProcessTree struct {
	parents map[int]int
	// commands is each process's executable, used to check that a pid still
	// belongs to the program that recorded it. Agents that leave a lock file
	// naming their pid can crash without removing it, and the operating
	// system eventually hands that pid to something unrelated.
	commands map[int]string
}

// SnapshotProcessTree reads the process table with one cancellable ps command.
// The caller's context is authoritative; commandTimeout is only a second line
// of defence for callers that supplied no deadline of their own. Within a
// discovery sweep the table is read once, on the sweep's context; see
// WithSweep.
func SnapshotProcessTree(ctx context.Context) (*ProcessTree, error) {
	if s := sweepOf(ctx); s != nil {
		return s.processTree()
	}
	return snapshotProcessTree(ctx)
}

func snapshotProcessTree(ctx context.Context) (*ProcessTree, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,comm=").Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return parseProcessTree(string(out)), nil
}

// ProcessTreeFromTable builds a snapshot from `ps -axo pid=,ppid=,comm=`
// output. It exists so adapters can be tested against a process table they
// describe, rather than whatever happens to be running on the test machine.
func ProcessTreeFromTable(table string) *ProcessTree { return parseProcessTree(table) }

func parseProcessTree(table string) *ProcessTree {
	parents := make(map[int]int)
	commands := make(map[int]string)
	for _, line := range strings.Split(table, "\n") {
		first, rest := cutField(line)
		second, command := cutField(rest)
		if first == "" || second == "" {
			continue
		}
		pid, pidErr := strconv.Atoi(first)
		parent, parentErr := strconv.Atoi(second)
		if pidErr != nil || parentErr != nil || pid <= 0 || parent < 0 {
			continue
		}
		parents[pid] = parent
		// The command is everything after the second column, not a third
		// field: macOS reports the full executable path, and paths like
		// ".../Application Support/..." contain spaces.
		if command = strings.TrimSpace(command); command != "" {
			commands[pid] = command
		}
	}
	return &ProcessTree{parents: parents, commands: commands}
}

// cutField splits off the first whitespace-delimited field of s.
func cutField(s string) (field, rest string) {
	s = strings.TrimLeft(s, " \t")
	if end := strings.IndexAny(s, " \t"); end >= 0 {
		return s[:end], s[end:]
	}
	return s, ""
}

// PIDs lists every process whose command the snapshot knows, in ascending
// order so callers iterate deterministically.
func (p *ProcessTree) PIDs() []int {
	if p == nil {
		return nil
	}
	pids := make([]int, 0, len(p.commands))
	for pid := range p.commands {
		pids = append(pids, pid)
	}
	slices.Sort(pids)
	return pids
}

// Command returns the executable a pid was running in this snapshot, or ""
// when the snapshot does not know it.
func (p *ProcessTree) Command(pid int) string {
	if p == nil {
		return ""
	}
	return p.commands[pid]
}

// OwnsPID reports whether pid is the pane process or one of its descendants in
// this snapshot. A short depth bound protects against malformed/cyclic process
// data and preserves the previous lookup's conservative behaviour.
func (p *ProcessTree) OwnsPID(panePID, pid int) bool {
	const maxDepth = 12 // guards against a cycle in a malformed process table
	for range maxDepth {
		if pid <= 1 {
			return false
		}
		if pid == panePID {
			return true
		}
		if p == nil {
			return false
		}
		parent, ok := p.parents[pid]
		if !ok || parent == pid {
			return false
		}
		pid = parent
	}
	return false
}

// SocketEnv names a private tmux server for this process to use instead of
// the default one.
//
// It exists for tests. An agent working on this repository runs inside a
// tmux pane, so $TMUX is set, and a bare tmux command — from a test, a probe,
// anything — goes to the server that pane belongs to, whatever TMUX_TMPDIR
// says. That is the user's own server, with their shells and every agent
// session on it. A test helper that ran kill-server there once took all of it
// down. With this set, every command this package runs carries -S and so can
// only ever reach the named socket. See tmuxtest.Isolate.
const SocketEnv = "AGENTMAN_TMUX_SOCKET"

// socketArgs are the global flags that pin a command to SocketEnv's server,
// or nothing when it is unset.
func socketArgs() []string {
	if socket := os.Getenv(SocketEnv); socket != "" {
		return []string{"-S", socket}
	}
	return nil
}

func run(ctx context.Context, args ...string) (string, error) {
	// Whatever a sweep captured of a pane is out of date once something is
	// sent to it.
	if s := sweepOf(ctx); s != nil {
		if target, ok := sendsTo(args); ok {
			s.forget(target)
		}
	}
	out, err := runOutput(ctx, args...)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("%s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	return out, nil
}

// runOutput runs one tmux command and returns what it printed, including
// when it failed partway through a sequence of commands.
func runOutput(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", append(socketArgs(), args...)...).Output()
	return string(out), err
}

// NewName mints a session name for an agent launch.
func NewName(kind string) string {
	return fmt.Sprintf("%s%s-%d-%s", Prefix, kind, time.Now().UnixMilli(), uniqueSuffix())
}

func uniqueSuffix() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	// Randomness is only for collision avoidance, not authentication. A
	// nanosecond timestamp plus pid remains a useful fallback if the OS random
	// source is temporarily unavailable.
	return fmt.Sprintf("%x-%x", time.Now().UnixNano(), os.Getpid())
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
	if s := sweepOf(ctx); s != nil {
		if pane, ok := s.capture(name); ok {
			return pane, nil
		}
	}
	return captureFresh(ctx, name)
}

func captureFresh(ctx context.Context, name string) (string, error) {
	// -p prints to stdout; without -S the capture is the visible pane only,
	// which is exactly the region a prompt occupies.
	out, err := run(ctx, "capture-pane", "-t", name, "-p")
	if err != nil {
		return "", err
	}
	return out, nil
}

// CaptureScrollback returns the pane plus up to lines of history above it.
//
// Capture is the right tool for a prompt, which always sits at the bottom of
// the visible pane. A reply being written is not: it scrolls, and by the time
// it is worth reading most of it is above the fold. An agent that renders to
// the normal screen keeps that history in tmux, and this is how to reach it.
func CaptureScrollback(ctx context.Context, name string, lines int) (string, error) {
	if !Available() {
		return "", ErrNotInstalled
	}
	if lines <= 0 {
		return Capture(ctx, name)
	}
	out, err := run(ctx, "capture-pane", "-t", name, "-p", "-S", "-"+strconv.Itoa(lines))
	if err != nil {
		return "", err
	}
	return out, nil
}

// RevealCodexQuestion opens Codex's collapsed async question tray when it is
// the active bottom-of-pane control. Capture and the key are serialized with
// other Agentman terminal actions so a concurrent phone send cannot receive
// the shortcut instead. A normal pane is left untouched.
func RevealCodexQuestion(ctx context.Context, name string) (string, error) {
	lock := actionLock(name)
	lock.Lock()
	defer lock.Unlock()
	trayShown := func(pane string) bool {
		return question.Detect(pane) == nil && question.CodexQueued(pane)
	}
	pane, err := Capture(ctx, name)
	if err != nil || !trayShown(pane) {
		return pane, err
	}
	// A key is about to be pressed, so it goes on the pane as it is now.
	// Within a discovery sweep the capture above is from the start of the
	// sweep, and the phone may have answered the question since.
	pane, err = captureFresh(ctx, name)
	if err != nil || !trayShown(pane) {
		return pane, err
	}
	if _, err := run(ctx, "send-keys", "-t", name, "S-Left"); err != nil {
		return "", fmt.Errorf("tmux: could not reveal Codex question: %w", err)
	}
	time.Sleep(80 * time.Millisecond)
	return captureFresh(ctx, name)
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
	lock := actionLock(name)
	lock.Lock()
	defer lock.Unlock()
	if _, err := run(ctx, "send-keys", "-t", name, "-l", "--", key); err != nil {
		return fmt.Errorf("tmux: could not answer: %w", err)
	}
	return nil
}

// AnswerCodexCustom fills the current Codex follow-up question's Other field.
// Codex needs Enter to open the field and commit its text. Some layouts then
// submit immediately; others need a final Enter. Verify the pane before
// sending that final Enter so it cannot act on an unrelated composer.
func AnswerCodexCustom(ctx context.Context, name, key, text string) error {
	text = strings.TrimSpace(text)
	if text == "" || strings.ContainsAny(text, "\r\n") || len([]rune(text)) > 200 {
		return errors.New("tmux: Codex custom answers must be one line of at most 200 characters")
	}
	lock := actionLock(name)
	lock.Lock()
	defer lock.Unlock()
	pane, err := Capture(ctx, name)
	if err != nil {
		return err
	}
	initial := question.Detect(pane)
	if initial == nil || !initial.Custom || initial.CustomKey != key {
		return errors.New("tmux: Codex custom question is no longer current")
	}
	if err := sendLiteral(ctx, name, key); err != nil {
		return fmt.Errorf("tmux: could not select Codex custom answer: %w", err)
	}
	time.Sleep(45 * time.Millisecond)
	if _, err := run(ctx, "send-keys", "-t", name, "Enter"); err != nil {
		return fmt.Errorf("tmux: could not open Codex custom answer: %w", err)
	}
	time.Sleep(45 * time.Millisecond)
	if err := sendLiteral(ctx, name, text); err != nil {
		return fmt.Errorf("tmux: could not type Codex custom answer: %w", err)
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := run(ctx, "send-keys", "-t", name, "Enter"); err != nil {
		return fmt.Errorf("tmux: could not commit Codex custom answer: %w", err)
	}
	time.Sleep(60 * time.Millisecond)
	pane, err = Capture(ctx, name)
	if err != nil {
		return err
	}
	committed := question.Detect(pane)
	if committed == nil && !question.CodexQueued(pane) && codexAnswerEchoed(pane, text) {
		// Some current Codex layouts submit on the first Enter after text;
		// sending another would act on an unrelated composer.
		return nil
	}
	prefix := []rune(text)
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	matched := false
	if committed != nil && committed.Prompt == initial.Prompt {
		for _, option := range committed.Options {
			if option.Key == key && option.Selected && strings.HasPrefix(option.Label, string(prefix)) {
				matched = true
			}
		}
	}
	if !matched {
		return errors.New("tmux: Codex did not show the custom answer; finish it in the terminal")
	}
	if _, err := run(ctx, "send-keys", "-t", name, "Enter"); err != nil {
		return fmt.Errorf("tmux: could not submit Codex custom answer: %w", err)
	}
	return nil
}

func codexAnswerEchoed(pane, text string) bool {
	lines := strings.Split(pane, "\n")
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-16; i-- {
		if strings.TrimSpace(lines[i]) != text {
			continue
		}
		for j := i - 1; j >= 0 && j >= i-5; j-- {
			if strings.Contains(lines[j], "› > ") {
				return true
			}
		}
	}
	return false
}

// AnswerWorkspaceTrust selects Claude's unnumbered first-run folder prompt.
// Verify the exact active control after moving focus; Enter on a stale pane
// could otherwise approve a different action that appeared in the meantime.
func AnswerWorkspaceTrust(ctx context.Context, name, choice string, focusDistance int) error {
	if choice != "yes" && choice != "no" {
		return errors.New("tmux: invalid workspace trust choice")
	}
	lock := actionLock(name)
	lock.Lock()
	defer lock.Unlock()
	if err := moveFocus(ctx, name, focusDistance); err != nil {
		return err
	}
	if focusDistance != 0 {
		time.Sleep(45 * time.Millisecond)
	}
	pane, err := Capture(ctx, name)
	if err != nil {
		return err
	}
	label := "Yes, I trust this folder"
	if choice == "no" {
		label = "No, exit"
	}
	lines := strings.Split(strings.TrimSpace(pane), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[len(lines)-1]) != "Enter to confirm · Esc to cancel" ||
		!strings.Contains(pane, "Accessing workspace:") ||
		!regexp.MustCompile(`(?m)^\s*❯\s*`+regexp.QuoteMeta(label)+`\s*$`).MatchString(
			strings.Join(lines[max(0, len(lines)-8):], "\n")) {
		return errors.New("tmux: Claude's workspace trust choice changed; refresh the session")
	}
	if _, err := run(ctx, "send-keys", "-t", name, "Enter"); err != nil {
		return fmt.Errorf("tmux: could not answer workspace trust: %w", err)
	}
	return nil
}

// AnswerArrowMenu answers a menu with no numbers to type: it moves focus by
// distance rows, then presses Enter only if focused confirms, from a fresh
// capture, that the intended row now carries the cursor.
//
// The check is what makes this safe to drive from a phone. The menu on screen
// can change between the moment the phone read it and the moment its answer
// arrives — the agent may have moved on, or a different prompt may have taken
// its place — and pressing Enter on whatever row happens to be focused then
// approves something the user never saw.
func AnswerArrowMenu(ctx context.Context, name string, distance int, focused func(pane string) bool) error {
	if !Available() {
		return ErrNotInstalled
	}
	lock := actionLock(name)
	lock.Lock()
	defer lock.Unlock()
	if err := moveFocus(ctx, name, distance); err != nil {
		return err
	}
	if distance != 0 {
		time.Sleep(45 * time.Millisecond)
	}
	pane, err := Capture(ctx, name)
	if err != nil {
		return err
	}
	if !focused(pane) {
		return errors.New("tmux: that choice is no longer on screen; refresh the session")
	}
	if _, err := run(ctx, "send-keys", "-t", name, "Enter"); err != nil {
		return fmt.Errorf("tmux: could not answer: %w", err)
	}
	return nil
}

// AnswerWithNote chooses a menu option and sends a note with it, the way
// Claude Code takes one: focus the option, press Tab to open a line for the
// note ("No, and tell Claude what to do differently"), type the note, press
// Enter.
//
// Each step is checked against a fresh capture before the next, under the
// pane's lock: focused before Tab, amending before typing, typed before
// Enter. Pressing Enter on the wrong row, or typing into whatever else the
// screen became, would send something the user did not choose.
func AnswerWithNote(
	ctx context.Context, name string, distance int, note string,
	focused, amending, typed func(pane string) bool,
) error {
	if !Available() {
		return ErrNotInstalled
	}
	// One line: the note is typed into a single-line field, where a newline
	// would submit it early. The app sends one line already.
	note = strings.Join(strings.Fields(note), " ")
	if note == "" {
		return errors.New("tmux: the note is empty")
	}
	lock := actionLock(name)
	lock.Lock()
	defer lock.Unlock()
	if err := moveFocus(ctx, name, distance); err != nil {
		return err
	}
	if err := awaitPane(ctx, name, focused); err != nil {
		return errors.New("tmux: that choice is no longer on screen; refresh the session")
	}
	if _, err := run(ctx, "send-keys", "-t", name, "Tab"); err != nil {
		return fmt.Errorf("tmux: could not open the note: %w", err)
	}
	if err := awaitPane(ctx, name, amending); err != nil {
		return errors.New("tmux: the note could not be opened; answer it in the terminal")
	}
	if err := sendLiteral(ctx, name, note); err != nil {
		return fmt.Errorf("tmux: could not type the note: %w", err)
	}
	if err := awaitPane(ctx, name, typed); err != nil {
		return errors.New("tmux: the note did not appear; answer it in the terminal")
	}
	if _, err := run(ctx, "send-keys", "-t", name, "Enter"); err != nil {
		return fmt.Errorf("tmux: could not answer: %w", err)
	}
	return nil
}

// awaitPane captures the pane until check accepts it, for a few render ticks:
// an Ink TUI redraws a moment after the key that changed it.
func awaitPane(ctx context.Context, name string, check func(pane string) bool) error {
	for attempt := 0; attempt < 5; attempt++ {
		time.Sleep(45 * time.Millisecond)
		pane, err := Capture(ctx, name)
		if err != nil {
			return err
		}
		if check(pane) {
			return nil
		}
	}
	return errors.New("tmux: the pane did not show what was expected")
}

// AnswerSingleForm records a single choice in Claude's tabbed or preview
// question form. Those layouts do not accept numeric shortcuts: the desired
// row has to receive focus and Enter before Tab can advance the form.
//
// The visible-focus check is deliberately inside the same per-pane lock as
// the arrow and Enter events. Pressing Enter on the wrong row would submit a
// different answer, so an uncertain render is safer to reject than to guess.
func AnswerSingleForm(ctx context.Context, name, key string, focusDistance int, advance bool) error {
	if !Available() {
		return ErrNotInstalled
	}
	if key == "" {
		return errors.New("tmux: no option given")
	}
	lock := actionLock(name)
	lock.Lock()
	defer lock.Unlock()

	if err := moveFocus(ctx, name, focusDistance); err != nil {
		return err
	}
	if focusDistance != 0 {
		time.Sleep(45 * time.Millisecond)
	}
	if err := verifyFocusedOption(ctx, name, key); err != nil {
		return err
	}
	if _, err := run(ctx, "send-keys", "-t", name, "Enter"); err != nil {
		return fmt.Errorf("tmux: could not select Claude option: %w", err)
	}
	if advance {
		time.Sleep(60 * time.Millisecond)
		return advanceClaudeQuestion(ctx, name)
	}
	return nil
}

// AnswerCustom chooses Claude's synthetic free-text row, types the response,
// and submits it. This has to remain one locked action: another phone request
// interleaved between the row selection and the text would answer the wrong
// control.
func AnswerCustom(ctx context.Context, name, key, text string) error {
	return answerCustom(ctx, name, key, text, false)
}

// AnswerCustomAndAdvance records custom text in a multi-question tab form and
// moves to the next tab after Claude accepts the text.
func AnswerCustomAndAdvance(ctx context.Context, name, key, text string) error {
	return answerCustom(ctx, name, key, text, true)
}

func answerCustom(ctx context.Context, name, key, text string, advance bool) error {
	if !Available() {
		return ErrNotInstalled
	}
	if key == "" {
		return errors.New("tmux: no custom option given")
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("tmux: refusing to send an empty custom answer")
	}
	lock := actionLock(name)
	lock.Lock()
	defer lock.Unlock()

	if err := sendLiteral(ctx, name, key); err != nil {
		return fmt.Errorf("tmux: could not select custom answer: %w", err)
	}
	time.Sleep(40 * time.Millisecond)
	if err := typeAnswerText(ctx, name, text); err != nil {
		return err
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := run(ctx, "send-keys", "-t", name, "Enter"); err != nil {
		return fmt.Errorf("tmux: could not submit custom answer: %w", err)
	}
	if advance {
		time.Sleep(60 * time.Millisecond)
		return advanceClaudeQuestion(ctx, name)
	}
	return nil
}

func advanceClaudeQuestion(ctx context.Context, name string) error {
	pane, err := Capture(ctx, name)
	if err != nil {
		return fmt.Errorf("tmux: could not verify Claude question navigation: %w", err)
	}
	if !hasClaudeQuestionTabs(pane) {
		// A one-question form can finish immediately after selection. In that
		// case there is no tab strip left and sending Tab would touch the normal
		// prompt, so completion is already the desired result.
		return nil
	}
	if _, err := run(ctx, "send-keys", "-t", name, "Tab"); err != nil {
		return fmt.Errorf("tmux: could not advance to the next Claude question: %w", err)
	}
	return nil
}

func hasClaudeQuestionTabs(pane string) bool {
	hints := strings.ToLower(strings.Join(strings.Fields(pane), " "))
	return strings.Contains(hints, "tab to switch questions")
}

func verifyFocusedOption(ctx context.Context, name, key string) error {
	focusedOption := regexp.MustCompile(
		`(?m)^\s*[❯›>»▸▶→*]\s*` + regexp.QuoteMeta(key) + `[.)]`,
	)
	for attempt := 0; attempt < 3; attempt++ {
		pane, err := Capture(ctx, name)
		if err != nil {
			return fmt.Errorf("tmux: could not verify Claude option focus: %w", err)
		}
		if liveFormFooter.MatchString(pane) && focusedOption.MatchString(pane) {
			return nil
		}
		if attempt < 2 {
			time.Sleep(40 * time.Millisecond)
		}
	}
	return fmt.Errorf("tmux: option %q did not receive focus; refusing to press Enter", key)
}

// AnswerForm reconciles a Claude multi-select menu with the answer selected on
// the phone, then activates Next/Submit. safeMove first leaves Claude's custom
// input when checkbox digits need to be sent. targetMove then reaches either
// Submit or the custom input; afterTextMove reaches Submit after typing.
func AnswerForm(
	ctx context.Context,
	name string,
	toggleKeys []string,
	safeMove, targetMove, afterTextMove int,
	text string,
) error {
	if !Available() {
		return ErrNotInstalled
	}
	lock := actionLock(name)
	lock.Lock()
	defer lock.Unlock()

	if err := moveFocus(ctx, name, safeMove); err != nil {
		return err
	}
	if safeMove != 0 {
		time.Sleep(35 * time.Millisecond)
	}
	for _, key := range toggleKeys {
		if key == "" {
			return errors.New("tmux: empty multi-select option")
		}
		if err := sendLiteral(ctx, name, key); err != nil {
			return fmt.Errorf("tmux: could not toggle option %q: %w", key, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := moveFocus(ctx, name, targetMove); err != nil {
		return err
	}
	if text != "" {
		time.Sleep(35 * time.Millisecond)
		if err := typeAnswerText(ctx, name, text); err != nil {
			return err
		}
		time.Sleep(40 * time.Millisecond)
		if err := moveFocus(ctx, name, afterTextMove); err != nil {
			return err
		}
	}
	time.Sleep(60 * time.Millisecond)
	if err := verifyFormSubmitFocus(ctx, name); err != nil {
		return err
	}
	if _, err := run(ctx, "send-keys", "-t", name, "Enter"); err != nil {
		return fmt.Errorf("tmux: could not submit multi-select answer: %w", err)
	}
	return nil
}

func sendLiteral(ctx context.Context, name, text string) error {
	_, err := run(ctx, "send-keys", "-t", name, "-l", "--", text)
	return err
}

func typeAnswerText(ctx context.Context, name, text string) error {
	if strings.ContainsAny(text, "\n\r") {
		if err := pasteMultiline(ctx, name, text); err != nil {
			return fmt.Errorf("tmux: could not type custom answer: %w", err)
		}
		return nil
	}
	if err := sendLiteral(ctx, name, text); err != nil {
		return fmt.Errorf("tmux: could not type custom answer: %w", err)
	}
	return nil
}

func moveFocus(ctx context.Context, name string, distance int) error {
	if distance == 0 {
		return nil
	}
	key := "Down"
	if distance < 0 {
		key = "Up"
		distance = -distance
	}
	if distance > 256 {
		return errors.New("tmux: refusing an implausibly large menu navigation")
	}
	// Ink/React TUIs update focus between key events. tmux's -N emits a burst
	// quickly enough that every event can observe the same stale focus and land
	// on the wrong row. Send discrete events and allow one render tick between
	// them; the captured Claude failure otherwise ended by pressing Enter on
	// option 1 and toggling it instead of activating Next.
	for step := 0; step < distance; step++ {
		if _, err := run(ctx, "send-keys", "-t", name, key); err != nil {
			return fmt.Errorf("tmux: could not move to form control: %w", err)
		}
		if step+1 < distance {
			time.Sleep(30 * time.Millisecond)
		}
	}
	return nil
}

// verifyFormSubmitFocus is the final safety rail before Enter. In tests and
// non-Claude sinks there is no form footer, so there is nothing to verify. On
// a real Claude checkbox form, however, Enter is only safe when Next/Submit is
// visibly focused; anywhere else it toggles the current checkbox.
func verifyFormSubmitFocus(ctx context.Context, name string) error {
	for attempt := 0; attempt < 3; attempt++ {
		pane, err := Capture(ctx, name)
		if err != nil {
			return fmt.Errorf("tmux: could not verify multi-select focus: %w", err)
		}
		if !liveFormFooter.MatchString(pane) || focusedFormControl.MatchString(pane) {
			return nil
		}
		// capture-pane can win the race with Ink's redraw even though the key
		// event was accepted. Give it two render ticks before concluding that
		// the navigation genuinely landed on an option.
		if attempt < 2 {
			time.Sleep(40 * time.Millisecond)
		}
	}
	return errors.New("tmux: Next/Submit did not receive focus; refusing to press Enter")
}
