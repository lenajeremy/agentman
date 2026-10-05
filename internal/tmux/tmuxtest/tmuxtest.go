// Package tmuxtest keeps tests off the developer's own tmux server.
//
// Anyone working on this repository from inside tmux — which includes every
// coding agent Agentman manages — has $TMUX set, and a bare tmux command goes
// to the server that names, whatever TMUX_TMPDIR says. That server holds the
// person's shells and every agent session they have open. A throwaway test
// once ended its run with kill-server and took all of it down.
//
// Every package whose tests can reach tmux, directly or through discovery,
// runs them against a private server instead:
//
//	func TestMain(m *testing.M) { os.Exit(tmuxtest.Run(m)) }
package tmuxtest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/tmux"
)

// Run isolates tmux for the whole test binary, runs its tests, and cleans up.
func Run(m *testing.M) int {
	cleanup, err := Isolate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tmuxtest:", err)
		return 1
	}
	defer cleanup()
	return m.Run()
}

// Isolate points every tmux command this process runs at a private server and
// returns a function that ends the sessions left on it.
//
// The tmux package pins its own commands to the socket through SocketEnv.
// TMUX is also unset and TMUX_TMPDIR made private, so that anything running
// tmux some other way finds an empty private default server rather than the
// user's.
//
// A socket the developer already named is respected, untouched: that is how
// the opt-in live tests (AGENTMAN_LIVE_CLAUDE_FORM and friends) reach the
// disposable pane they were pointed at, and a server this process did not
// create is never cleaned up.
func Isolate() (func(), error) {
	if os.Getenv(tmux.SocketEnv) != "" {
		return func() {}, nil
	}
	// Kept short: a unix socket path may not exceed 104 bytes on macOS.
	dir, err := os.MkdirTemp("", "amtmux")
	if err != nil {
		return nil, err
	}
	socket := filepath.Join(dir, "sock")
	if err := os.Setenv(tmux.SocketEnv, socket); err != nil {
		return nil, err
	}
	if err := os.Unsetenv("TMUX"); err != nil {
		return nil, err
	}
	if err := os.Setenv("TMUX_TMPDIR", dir); err != nil {
		return nil, err
	}
	return func() {
		// Never kill-server, not even here: end each session on this socket by
		// its exact name, and the server exits on its own once the last one is
		// gone.
		out, _ := exec.Command("tmux", "-S", socket, "list-sessions", "-F", "#{session_name}").Output()
		for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if name != "" {
				_ = exec.Command("tmux", "-S", socket, "kill-session", "-t", "="+name).Run()
			}
		}
		_ = os.RemoveAll(dir)
	}, nil
}
