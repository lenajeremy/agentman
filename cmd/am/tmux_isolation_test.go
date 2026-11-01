package main

import (
	"errors"
	"os"
	"testing"

	"github.com/lenajeremy/agentman/internal/tmux/tmuxtest"
)

// Every tmux command these tests reach runs on a private server, never the
// one the developer is working in. See tmuxtest.
//
// launchd is the same whatever HOME a test sets, so no test reaches the
// real launchctl either, which would act on the developer's own service, and
// none finds or signals a daemon they started: tests that need either put
// in fakes of their own.
func TestMain(m *testing.M) {
	launchctlCommand = func(...string) ([]byte, error) {
		return nil, errors.New("tests never reach the real launchctl")
	}
	findTerminalDaemon = func(string) int { return 0 }
	stopProcess = func(int) error { return errors.New("tests never signal a real process") }
	os.Exit(tmuxtest.Run(m))
}
