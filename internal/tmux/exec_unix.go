//go:build unix

package tmux

import "syscall"

// syscallExec replaces the current process image.
//
// Isolated behind a build tag because Windows has no equivalent; a port would
// fall back to running tmux as a child process (or, more likely, not support
// the tmux path at all).
func syscallExec(binary string, args, env []string) error {
	return syscall.Exec(binary, args, env)
}
