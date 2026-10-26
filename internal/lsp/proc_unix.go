//go:build unix

package lsp

import (
	"os/exec"
	"syscall"
)

// inGroup starts the server in a process group of its own, so stopping it
// also stops what it started (the TypeScript server's tsserver, gopls's go).
func inGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
