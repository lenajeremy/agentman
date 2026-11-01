//go:build unix

package limits

import (
	"os/exec"
	"syscall"
)

// inGroup starts Claude Code in a process group of its own, so stopping it
// also stops anything it started.
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
