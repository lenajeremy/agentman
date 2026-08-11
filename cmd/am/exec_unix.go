//go:build unix

package main

import "syscall"

// syscallExec replaces this process with another program, so the wrapper does
// not sit between the user and their agent's terminal.
func syscallExec(binary string, args, env []string) error {
	return syscall.Exec(binary, args, env)
}
