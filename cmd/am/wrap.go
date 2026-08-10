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
	binary, err := exec.LookPath(agent)
	if err != nil {
		return fmt.Errorf("%s is not installed (or not on PATH)", agent)
	}
	if !tmux.Available() {
		return fmt.Errorf(
			"tmux is required to send messages to a session — install it with `brew install tmux`, "+
				"or run `%s` directly to use it without sending", agent)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	name := tmux.NewName(agent)
	command := append([]string{binary}, args...)

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
