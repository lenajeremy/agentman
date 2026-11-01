package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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
		if err := takeMuseBack(ctx, id, os.Stdin, os.Stderr); err != nil {
			return err
		}
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

// takeMuseBack makes the daemon let go of a session before this terminal
// opens it. Muse will not hand over a session mid-turn, so when Agentman is
// running one (perhaps waiting on an approval) the person is asked whether
// to stop it; without a terminal to ask, the turn is left to finish.
func takeMuseBack(ctx context.Context, id string, in io.Reader, out io.Writer) error {
	err := releaseMuse(ctx, id, false)
	if err == nil || err.Error() != source.ErrMuseWorking.Error() {
		// Not running, or not answering: Muse itself says if the session
		// is still open elsewhere.
		return nil
	}
	if !stdinIsTerminal() {
		return errors.New("Agentman is running a turn in this session; open it once that turn is done, or stop it from the phone")
	}
	fmt.Fprint(out, "Agentman is running a turn in this session (it may be waiting on an approval).\nStop it and open the session here? [y/N] ")
	answer, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		if err := releaseMuse(ctx, id, true); err != nil {
			return fmt.Errorf("could not take the session back: %w", err)
		}
		return nil
	}
	return errors.New("left the session with Agentman; it lets go when the turn is done")
}

// releaseMuse asks the running daemon to let go of a session, ending its
// turn first when stop says so. Without a daemon running there is nothing
// to let go of.
func releaseMuse(ctx context.Context, id string, stop bool) error {
	socket, err := socketPath()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	reply, err := socketCall(socket)(ctx, protocol.Request{Type: protocol.ReqReleaseSession, SessionID: "muse:" + id, Stop: stop})
	if err != nil {
		return nil
	}
	if reply.Type == protocol.EvtError {
		return errors.New(reply.Error)
	}
	return nil
}

// stdinIsTerminal says whether there is a person to ask; a variable so tests
// can be that person.
var stdinIsTerminal = func() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
