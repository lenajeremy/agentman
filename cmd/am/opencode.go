package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/source"
)

// openCodeProbeTimeout bounds the "is one already running?" check. It is a
// loopback request; if it has not answered by now, nothing is listening.
const openCodeProbeTimeout = 1500 * time.Millisecond

// runOpenCode starts OpenCode with its HTTP API where the daemon can find it.
//
// This exists because a plain `opencode` is invisible to agentman, in a way
// that gives the user no clue why. The TUI does serve the same HTTP API the
// adapter reads — but on an ephemeral port by default, so the daemon finds
// nothing on 4096 and the session simply never appears. That was the first
// thing anyone hit, and it looked like a bug in agentman rather than a missing
// flag.
//
// Unlike `am claude` and `am codex` there is no tmux here, deliberately.
// Those two need it because their CLIs have no input channel: without a
// terminal to type into, a message from the phone has nowhere to go. OpenCode
// accepts prompts over its API, so tmux would add a dependency and a layer of
// indirection while buying nothing at all.
func runOpenCode(ctx context.Context, args []string) error {
	binary, err := exec.LookPath("opencode")
	if err != nil {
		return fmt.Errorf("opencode is not installed (or not on PATH) — " +
			"install it with `brew install opencode`")
	}

	// A port the user chose wins. They may be running several projects, or
	// avoiding a clash; silently overriding that would be worse than the
	// session being missed, and the message below says how to reconcile it.
	if custom, ok := explicitPort(args); ok {
		fmt.Fprintf(os.Stderr, "%s\n", dim(fmt.Sprintf(
			"agentman: watching port %d, so this session will not appear — "+
				"run the daemon with AGENTMAN_OPENCODE_URL=http://127.0.0.1:%s to follow it",
			source.OpenCodeDefaultPort, custom)))
		return execOpenCode(binary, args)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d", source.OpenCodeDefaultPort)
	command := append([]string{"opencode", "--port", fmt.Sprint(source.OpenCodeDefaultPort)}, args...)

	// Something already holds the port. Starting a second server there cannot
	// work, but attaching to the first one is better than either failing or
	// falling back to an invisible port: both TUIs then share one server, so
	// every session stays visible from the phone.
	if openCodeHealthy(ctx, url) {
		fmt.Fprintf(os.Stderr, "%s\n", dim("agentman: attaching to the OpenCode server already on "+url))
		command = append([]string{"opencode", "attach", url}, args...)
	} else if portBusy(source.OpenCodeDefaultPort) {
		return fmt.Errorf(
			"port %d is in use by something that is not OpenCode.\n"+
				"Free it, or start OpenCode yourself on another port and point the daemon at it:\n"+
				"  opencode --port 4097\n"+
				"  AGENTMAN_OPENCODE_URL=http://127.0.0.1:4097 am serve",
			source.OpenCodeDefaultPort)
	} else {
		fmt.Fprintf(os.Stderr, "%s\n", dim("agentman: reachable from your phone (OpenCode API on "+url+")"))
	}

	return execOpenCode(binary, command[1:])
}

// execOpenCode replaces this process with OpenCode.
//
// Exec rather than a subprocess so the user gets the TUI's own terminal
// handling — signals, resizes, and mouse reporting all behave exactly as they
// would without the wrapper, which is the whole point of a wrapper.
func execOpenCode(binary string, args []string) error {
	return syscallExec(binary, append([]string{"opencode"}, args...), os.Environ())
}

// explicitPort reports a --port the user passed, so it can be respected.
func explicitPort(args []string) (string, bool) {
	for i, arg := range args {
		switch {
		case arg == "--port", arg == "-port":
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", false
		case strings.HasPrefix(arg, "--port="):
			return strings.TrimPrefix(arg, "--port="), true
		case strings.HasPrefix(arg, "-port="):
			return strings.TrimPrefix(arg, "-port="), true
		}
	}
	return "", false
}

// openCodeHealthy reports whether an OpenCode server is answering at url.
//
// The health endpoint rather than a bare connect: something else on the port
// would accept the connection just as readily, and attaching to it would fail
// in a way that points nowhere near the cause.
func openCodeHealthy(ctx context.Context, url string) bool {
	ctx, cancel := context.WithTimeout(ctx, openCodeProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/global/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
