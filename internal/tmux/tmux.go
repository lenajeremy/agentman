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
	"strconv"
	"strings"
	"sync"
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
	if pid > 1 && pid == panePID {
		return true
	}
	processes, err := SnapshotProcessTree(context.Background())
	if err != nil {
		return false
	}
	return processes.OwnsPID(panePID, pid)
}

// ProcessTree is one immutable snapshot of the operating system's PID → parent
// relationships. A discovery sweep can perform any number of ancestry checks
// against it without spawning another process or observing an inconsistent
// process table halfway through the sweep.
type ProcessTree struct {
	parents map[int]int
}

// SnapshotProcessTree reads the process table with one cancellable ps command.
// The caller's context is authoritative; commandTimeout is only a second line
// of defence for callers that supplied no deadline of their own.
func SnapshotProcessTree(ctx context.Context) (*ProcessTree, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return parseProcessTree(string(out)), nil
}

func parseProcessTree(table string) *ProcessTree {
	parents := make(map[int]int)
	for _, line := range strings.Split(table, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if pidErr != nil || parentErr != nil || pid <= 0 || parent < 0 {
			continue
		}
		parents[pid] = parent
	}
	return &ProcessTree{parents: parents}
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
	lock := actionLock(name)
	lock.Lock()
	defer lock.Unlock()
	if _, err := run(ctx, "send-keys", "-t", name, "-l", key); err != nil {
		return fmt.Errorf("tmux: could not answer: %w", err)
	}
	return nil
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
	_, err := run(ctx, "send-keys", "-t", name, "-l", text)
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
