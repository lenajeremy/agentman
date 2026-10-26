package tmux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// AgentsSocket names the tmux server agents run on: Agentman's own, apart
// from the one the user's terminals share.
//
// Agents used to start on that shared server, the one a bare `tmux` reaches.
// Whatever ends it ends every agent with it: a terminal app tearing down what
// it started, a test or a script running kill-server, a setting in the user's
// tmux.conf. On 2026-10-26 quitting Ghostty took every agent down that way.
// On a server of its own, started by `am serve` rather than by a terminal,
// agents outlive the windows that show them, as the user expects.
const AgentsSocket = "agentman"

// LegacySocketEnv names a private server standing in for the shared one, in
// tests. See servers.
const LegacySocketEnv = "AGENTMAN_TMUX_LEGACY_SOCKET"

// server selects one tmux server by the flags that name it.
type server []string

// flags are what every command to this server carries: -u (see globalArgs)
// and the socket.
func (s server) flags() []string {
	return append([]string{"-u"}, s...)
}

// ours is the server agents are started on: AgentsSocket, or the private
// socket a test named.
func ours() server {
	if socket := os.Getenv(SocketEnv); socket != "" {
		return server{"-S", socket}
	}
	return server{"-L", AgentsSocket}
}

// servers are the ones agents are found on: ours, then the shared one, where
// agents started before they had their own keep running until they end. A
// test that names a private socket never reaches the shared server; it can
// name a second private one to stand in for it.
func servers() []server {
	list := []server{ours()}
	if socket := os.Getenv(LegacySocketEnv); socket != "" {
		list = append(list, server{"-S", socket})
	} else if os.Getenv(SocketEnv) == "" {
		list = append(list, server{})
	}
	return list
}

// SocketPath is where Agentman's own server listens, for anything that
// reaches it without this package: `tmux -S <path> attach`.
func SocketPath() string {
	if socket := os.Getenv(SocketEnv); socket != "" {
		return socket
	}
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), AgentsSocket)
}

// EnsureServer starts Agentman's server if it isn't running, and keeps it
// running: empty, with nobody attached, or with a tmux.conf that says
// otherwise. Whoever starts the server is what it belongs to, which is why
// `am claude` asks am serve to do this before starting an agent.
func EnsureServer(ctx context.Context) error {
	if !Available() {
		return ErrNotInstalled
	}
	// One run, so the server never sits empty with exit-empty on between
	// starting and being told to stay.
	_, err := runOn(ctx, ours(), "start-server", ";",
		"set-option", "-s", "exit-empty", "off", ";",
		"set-option", "-s", "exit-unattached", "off", ";",
		"set-option", "-g", "destroy-unattached", "off")
	if err != nil {
		return fmt.Errorf("tmux: could not start Agentman's server: %w", err)
	}
	return nil
}

// ServerPID is the process id of Agentman's server, or 0 when it isn't
// running.
func ServerPID(ctx context.Context) int {
	out, err := runOn(ctx, ours(), "display-message", "-p", "#{pid}")
	if err != nil {
		return 0
	}
	var pid int
	_, _ = fmt.Sscanf(strings.TrimSpace(out), "%d", &pid)
	return pid
}

// Where each session was last found. Commands to a session go to its server;
// a session not seen yet is one just started, which is on ours.
var (
	locatedMu sync.Mutex
	located   = map[string]server{}
)

func remember(name string, server server) {
	locatedMu.Lock()
	defer locatedMu.Unlock()
	located[name] = server
}

// rememberAll replaces what is known with a full listing, so ended sessions
// are forgotten.
func rememberAll(found map[string]server) {
	locatedMu.Lock()
	defer locatedMu.Unlock()
	located = found
}

func serverOf(name string) server {
	locatedMu.Lock()
	defer locatedMu.Unlock()
	if server, ok := located[name]; ok {
		return server
	}
	return ours()
}

// locate finds the server a session runs on, asking each when it hasn't been
// seen yet, as in a process of its own (`am attach`).
func locate(ctx context.Context, name string) server {
	locatedMu.Lock()
	_, ok := located[name]
	locatedMu.Unlock()
	if ok || Has(ctx, name) {
		return serverOf(name)
	}
	return ours()
}

// serverFor picks the server a command goes to by the session it targets:
// the -t of send-keys, capture-pane, kill-session and the rest, as
// "name", "=name" or "name:window.pane".
func serverFor(args []string) server {
	for index := 0; index+1 < len(args); index++ {
		if args[index] != "-t" {
			continue
		}
		target := strings.TrimPrefix(args[index+1], "=")
		if colon := strings.IndexByte(target, ':'); colon >= 0 {
			target = target[:colon]
		}
		return serverOf(target)
	}
	return ours()
}
