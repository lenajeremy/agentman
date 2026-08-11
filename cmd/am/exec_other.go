//go:build !unix

package main

import (
	"os"
	"os/exec"
)

// syscallExec falls back to a child process where exec is unavailable. The
// terminal experience is slightly worse — this process stays in the middle —
// but the wrapper still works rather than being unavailable entirely.
func syscallExec(binary string, args, env []string) error {
	cmd := exec.Command(binary, args[1:]...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
