package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

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

	name := tmux.NewName(agent)
	command := append(append([]string{binary}, prefix...), args...)

	if err := tmux.Launch(ctx, name, cwd, command); err != nil {
		return err
	}

	// The agent needs a moment to register itself before the daemon can match
	// it; attaching immediately is fine, this is only for the message below.
	time.Sleep(150 * time.Millisecond)
	fmt.Fprintf(os.Stderr, "%s\n", dim("agentman: reachable from your phone (tmux "+name+")"))

	// Replace this process with the tmux client so the user gets tmux's own
	// terminal handling — signals, resizes, and scrollback all behave normally.
	return tmux.Attach(name)
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
