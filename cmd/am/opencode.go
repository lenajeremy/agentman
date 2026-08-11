package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
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
		port, parseErr := strconv.Atoi(custom)
		if parseErr == nil && port >= source.OpenCodeDefaultPort &&
			port < source.OpenCodeDefaultPort+source.OpenCodePortSpan {
			fmt.Fprintf(os.Stderr, "%s\n", dim(fmt.Sprintf(
				"agentman: reachable from your phone (OpenCode API on http://127.0.0.1:%d)", port)))
		} else {
			fmt.Fprintf(os.Stderr, "%s\n", dim(fmt.Sprintf(
				"agentman: ports %d-%d are watched, so this session will not appear — "+
					"run the daemon with AGENTMAN_OPENCODE_URL=http://127.0.0.1:%s to follow it",
				source.OpenCodeDefaultPort,
				source.OpenCodeDefaultPort+source.OpenCodePortSpan-1,
				custom)))
		}
		return execOpenCode(binary, localOpenCodeArgs(args))
	}

	// Never attach to a server someone else started, however tempting it looks.
	//
	// A session belongs to the directory of the *server* that created it, not
	// the terminal you typed in — and attaching does not change that, `--dir`
	// included. So attaching filed every session under whatever directory the
	// existing server happened to be rooted in: you would open OpenCode in your
	// project, and the session would show up on your phone under some unrelated
	// path with none of your work in it.
	//
	// Starting our own server in this directory is what makes the session say
	// where it actually came from. Each concurrent invocation takes a free port
	// in the watched range, and the daemon merges the sessions from all of them.
	port := freeOpenCodePort()
	if port == 0 {
		return fmt.Errorf(
			"no free port for OpenCode between %d and %d — close some sessions and try again",
			source.OpenCodeDefaultPort, source.OpenCodeDefaultPort+source.OpenCodePortSpan-1)
	}

	if port == source.OpenCodeDefaultPort {
		fmt.Fprintf(os.Stderr, "%s\n", dim(fmt.Sprintf(
			"agentman: reachable from your phone (OpenCode API on http://127.0.0.1:%d)", port)))
	} else {
		// A different port is fine and needs no action: the daemon reads the
		// shared session store through whichever server answers.
		fmt.Fprintf(os.Stderr, "%s\n", dim(fmt.Sprintf(
			"agentman: reachable from your phone (OpenCode API on http://127.0.0.1:%d — "+
				"%d was taken)", port, source.OpenCodeDefaultPort)))
	}

	command := append([]string{"--port", fmt.Sprint(port)}, localOpenCodeArgs(args)...)
	return execOpenCode(binary, command)
}

// freeOpenCodePort returns a usable port, preferring the one the daemon
// watches. Zero means they are all taken.
func freeOpenCodePort() int {
	for port := source.OpenCodeDefaultPort; port < source.OpenCodeDefaultPort+source.OpenCodePortSpan; port++ {
		if !portBusy(port) {
			return port
		}
	}
	return 0
}

// localOpenCodeArgs makes the API's trust boundary explicit. OpenCode already
// defaults to loopback, but spelling it out prevents a future default change
// from exposing prompts and approval endpoints to the LAN. A user-supplied
// hostname or mDNS flag is respected because it is an explicit opt-in.
func localOpenCodeArgs(args []string) []string {
	if hasOpenCodeNetworkFlag(args) {
		return args
	}
	out := make([]string, 0, len(args)+2)
	out = append(out, "--hostname", "127.0.0.1")
	return append(out, args...)
}

func hasOpenCodeNetworkFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--hostname" || strings.HasPrefix(arg, "--hostname=") ||
			arg == "--mdns" || strings.HasPrefix(arg, "--mdns=") {
			return true
		}
	}
	return false
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
