package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// attachPending gives every adapter that supports queued delivery the same
// queue, so a message survives whichever agent it was addressed to.
func attachPending(registry *source.Registry, queue *source.PendingQueue) {
	registry.EachSource(func(s source.Source) {
		if setter, ok := s.(interface{ SetPending(*source.PendingQueue) }); ok {
			setter.SetPending(queue)
		}
	})
}

// runWrap launches an agent CLI inside tmux so it can be typed into later.
//
// This is the difference between a session you can only watch and one you can
// actually reply to. A CLI started normally owns a terminal nobody else can
// write to; started through tmux, the daemon can type into it at any moment,
// including while the agent is mid-turn.
//
// The user's experience is meant to be unchanged: tmux is created detached and
// then attached to immediately, so `am claude` looks and feels like `claude`.
func runWrap(ctx context.Context, agent string, args []string) error {
	commandName := agent
	var prefix []string
	switch agent {
	case "cursor":
		commandName = "agent"
	case "kiro":
		// `kiro-cli` alone treats a first word as a subcommand, so a prompt
		// would be misread. `chat` takes the prompt and the flags people
		// actually reach for: --resume, --resume-id, --agent, --model.
		commandName, prefix = "kiro-cli", []string{"chat"}
	case "antigravity":
		commandName = "agy"
	}
	binary, err := exec.LookPath(commandName)
	if err != nil {
		return fmt.Errorf("%s is not installed (or not on PATH)", commandName)
	}
	if !tmux.Available() {
		return fmt.Errorf(
			"tmux is required to send messages to a session — install it with `brew install tmux`, "+
				"or run `%s` directly to use it without sending", commandName)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	args, err = resolveResumeFlag(agent, args)
	if err != nil {
		return err
	}
	if agent == "kiro" && !kiroResumes(args) {
		if home, err := os.UserHomeDir(); err == nil {
			prefix = append(prefix, hook.KiroAgentArgs(home, args)...)
		}
	}

	name := tmux.NewName(agent)
	command := append(append([]string{binary}, prefix...), args...)

	// The agent runs on Agentman's own tmux server, which am serve starts:
	// started from here instead, it would belong to this terminal, and
	// quitting the terminal app could end every agent on it.
	note := ownServer(ctx)
	if err := tmux.Launch(ctx, name, cwd, command, agentEnv(os.Environ())...); err != nil {
		return err
	}

	// The agent needs a moment to register itself before the daemon can match
	// it; attaching immediately is fine, this is only for the message below.
	time.Sleep(150 * time.Millisecond)
	fmt.Fprintf(os.Stderr, "%s\n", dim("agentman: reachable from your phone; keeps running if you close this window (am attach brings it back)"))
	if note != "" {
		fmt.Fprintf(os.Stderr, "%s\n", dim("agentman: "+note))
	}

	// Replace this process with the tmux client so the user gets tmux's own
	// terminal handling — signals, resizes, and scrollback all behave normally.
	return tmux.Attach(ctx, name)
}

// ownServer asks am serve to start the agents' tmux server, or starts it
// here when am serve isn't running. It returns what the user should know
// about where the agent runs, if anything.
func ownServer(ctx context.Context) string {
	path, err := socketPath()
	if err == nil {
		ask, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		event, err := socketCall(path)(ask, protocol.Request{Type: protocol.ReqAgentsTmux})
		if err == nil && event.Tmux != nil {
			if event.Tmux.Service {
				return ""
			}
			return "am serve was started from a terminal; am service install keeps agents running even after you quit every terminal app"
		}
	}
	if err := tmux.EnsureServer(ctx); err != nil {
		return err.Error()
	}
	return "am serve isn't running, so this agent belongs to this terminal app; am service install keeps agents running on their own"
}

// agentEnv is the environment an agent started from a terminal gets: the
// shell's own, as a command typed there would, but not what describes this
// terminal, since the agent runs in tmux, which sets those itself.
//
// Run from inside another agent's session (CLAUDECODE set: a shell Claude
// Code started), the shell's environment is that session's, full of markers
// that make the new agent its child: Claude then saves no transcript, and
// Agentman can't follow it. The agent gets am serve's environment instead,
// the login shell's, as if started from a fresh terminal.
func agentEnv(environ []string) []string {
	for _, entry := range environ {
		if value, ok := strings.CutPrefix(entry, "CLAUDECODE="); ok && value != "" {
			return nil
		}
	}
	var env []string
	for _, entry := range environ {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name == "" || name == "CLAUDECODE" || terminalVariable(name) {
			continue
		}
		env = append(env, entry)
	}
	return env
}

func terminalVariable(name string) bool {
	switch name {
	case "TERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "TERM_SESSION_ID", "TERMINFO", "COLORFGBG",
		"TMUX", "TMUX_PANE", "PWD", "OLDPWD", "SHLVL", "_", "WINDOWID",
		"LC_TERMINAL", "LC_TERMINAL_VERSION", "__CFBundleIdentifier", "SECURITYSESSIONID":
		return true
	}
	for _, prefix := range []string{"GHOSTTY_", "ITERM_", "KITTY_", "WEZTERM_", "ALACRITTY_", "VSCODE_", "WARP_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// kiroResumes reports whether arguments to `kiro-cli chat` reopen a session.
// A reopened session keeps the agent it ran as; starting it as Agentman's
// agent would switch it.
func kiroResumes(args []string) bool {
	for _, arg := range args {
		switch {
		case arg == "-r", arg == "--resume", arg == "--resume-id", arg == "--resume-picker",
			arg == "--list", strings.HasPrefix(arg, "--resume-id="):
			return true
		}
	}
	return false
}

// resolveResumeFlag rewrites `am <agent> --resume <id>` into whatever that
// CLI actually calls it.
//
// Every agent can reopen a session by id and every one spells it differently:
// Codex wants a `resume` subcommand, Kiro `--resume-id`, Antigravity
// `--conversation`. Typing the right one per agent is exactly the kind of
// thing nobody should have to remember, and the phone needs one spelling to
// send, so `--resume <id>` means the same thing everywhere here.
//
// A bare `--resume` with no id is left alone: several of these CLIs read that
// as "show me a picker", which is a reasonable thing to want from a terminal
// and is not ours to intercept.
// wrapKind maps a wrapper's agent word to the kind the rest of the program
// uses. Only cursor differs, and it matters: `am cursor` runs the Cursor CLI,
// whose sessions are KindCursorCLI. KindCursor is the IDE, which has no CLI
// to resume into at all.
func wrapKind(agent string) protocol.Kind {
	if agent == "cursor" {
		return protocol.KindCursorCLI
	}
	return protocol.Kind(agent)
}

func resolveResumeFlag(agent string, args []string) ([]string, error) {
	for i, arg := range args {
		if arg != "--resume" || i+1 >= len(args) {
			continue
		}
		id := args[i+1]
		if strings.HasPrefix(id, "-") {
			continue // the next word is another flag, so this is the picker
		}
		native, ok := source.ResumeArgs(wrapKind(agent), id)
		if !ok {
			return nil, fmt.Errorf("%s cannot resume a session by id", agent)
		}
		rewritten := append([]string{}, args[:i]...)
		rewritten = append(rewritten, native...)
		return append(rewritten, args[i+2:]...), nil
	}
	return args, nil
}

// runSend delivers a message to a running session from the terminal.
//
// The same path the phone uses, which is what makes injection testable before
// any app exists.
func runSend(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("send needs a session id and a message (see `am list`)")
	}
	sessionID, text := args[0], strings.Join(args[1:], " ")

	// Muse lets one connection drive a session at a time, and Agentman takes
	// a session only for the turn it sends. A one-off command that took it
	// would exit still holding it, so Muse is always sent to through the
	// daemon, whose one connection lets go when the turn ends.
	if strings.HasPrefix(sessionID, string(protocol.KindMuse)+":") {
		return sendThroughDaemon(ctx, sessionID, text)
	}

	registry, err := buildRegistry()
	if err != nil {
		return err
	}
	pending := source.NewPendingQueue()
	attachPending(registry, pending)

	// Discovery populates each adapter's session table, which Inject reads.
	if _, err := registry.Discover(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "am: warning: %v\n", err)
	}

	mode, err := registry.Inject(ctx, sessionID, text)
	if err != nil {
		return err
	}

	switch mode {
	case protocol.InjectTmux, protocol.InjectAPI:
		fmt.Printf("%s delivered\n", dim("✓"))
	case protocol.InjectHook:
		// Queued in this process, which is about to exit — so say what that
		// actually means rather than implying the message is on its way.
		fmt.Printf("%s %s\n", dim("·"),
			"this session has no live input channel; run `am serve` and send from there, "+
				"or restart it with `am claude` to send instantly")
	default:
		fmt.Printf("%s could not deliver\n", dim("✗"))
	}
	return nil
}

// runInterrupt exercises the same routed cancellation path as the phone.
func runInterrupt(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("interrupt needs one session id (see `am list`)")
	}
	registry, err := buildRegistry()
	if err != nil {
		return err
	}
	if _, err := registry.Discover(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "am: warning: %v\n", err)
	}
	if err := registry.Interrupt(ctx, args[0]); err != nil {
		return err
	}
	fmt.Printf("%s interrupted\n", dim("✓"))
	return nil
}

// runAnswer resolves one currently displayed choice through the same
// question-ID guarded path used by the phone.
func runAnswer(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("answer needs a session id and option key (see `am list -json`)")
	}
	registry, err := buildRegistry()
	if err != nil {
		return err
	}
	sessions, err := registry.Discover(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "am: warning: %v\n", err)
	}
	for _, session := range sessions {
		if session.ID != args[0] {
			continue
		}
		if session.Question == nil {
			return fmt.Errorf("session %s has no pending question", args[0])
		}
		if err := registry.Answer(ctx, session.ID, protocol.QuestionAnswer{
			QuestionID: session.Question.ID, OptionKey: args[1],
		}); err != nil {
			return err
		}
		fmt.Printf("%s answered\n", dim("✓"))
		return nil
	}
	return fmt.Errorf("unknown session %s", args[0])
}

// sendThroughDaemon hands a message to the running daemon to deliver.
func sendThroughDaemon(ctx context.Context, sessionID, text string) error {
	socket, err := socketPath()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	event, err := socketCall(socket)(ctx, protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: sessionID, Text: text,
		ClientID: fmt.Sprintf("am-send-%d", time.Now().UnixNano()),
	})
	if err != nil {
		return fmt.Errorf("Muse sessions are sent to through the daemon, and it isn't running: start it with `am serve` (or `am service install`)")
	}
	switch {
	case event.Type == protocol.EvtError:
		return errors.New(strings.TrimPrefix(event.Error, "daemon: "))
	case event.Status == protocol.StatusFailed:
		return errors.New(strings.TrimPrefix(firstNonEmptyString(event.Error, "could not deliver"), "daemon: "))
	}
	fmt.Printf("%s delivered\n", dim("✓"))
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
