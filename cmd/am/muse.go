package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// runMuse opens Meta Muse in this terminal, with no tmux: the phone and the
// Mac follow Muse through its own protocol. It runs Muse as a client of
// Muse's daemon, so the session can be watched live from the apps. Muse lets
// one client drive a session at a time, so for `am muse resume <id>` the
// daemon first lets go of that session if it was driving it; while this
// terminal has it, the apps watch, and they can send again once it closes.
func runMuse(ctx context.Context, args []string) error {
	binary, err := source.MuseBinary()
	if err != nil {
		return fmt.Errorf("muse is not installed — install it with `curl -fsSL https://dev.meta.ai/install.sh | bash`")
	}
	source.MuseEnsureDaemon(ctx, binary)
	if id := museResumeTarget(args); id != "" {
		releaseMuse(ctx, id)
	}
	fmt.Fprintf(os.Stderr, "%s\n", dim("agentman: watched live from your phone; close this session to send from there"))
	env := append(os.Environ(), "MUSE_EXPERIMENTAL_TUI_MSP_CLIENT=1")
	return syscallExec(binary, append([]string{binary}, args...), env)
}

// museResumeTarget is the session `muse resume <id>` opens, when it is named
// by its id. Muse's ids are UUIDs, and flags before it take values, so the
// id is the argument shaped like one.
func museResumeTarget(args []string) string {
	if len(args) < 2 || args[0] != "resume" {
		return ""
	}
	for _, arg := range args[1:] {
		if museUUID.MatchString(arg) {
			return strings.ToLower(arg)
		}
	}
	return ""
}

var museUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// releaseMuse asks the running daemon to let go of a session; without one
// running there is nothing to let go of.
func releaseMuse(ctx context.Context, id string) {
	socket, err := socketPath()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	_, _ = socketCall(socket)(ctx, protocol.Request{Type: protocol.ReqReleaseSession, SessionID: "muse:" + id})
}
