package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/lenajeremy/agentman/internal/tmux/tmuxtest"
)

// Every tmux command these tests reach runs on a private server, never the
// one the developer is working in. See tmuxtest.
//
// launchd is the same whatever HOME a test sets, so no test reaches the
// real launchctl either, which would act on the developer's own service;
// none finds or signals a daemon they started, or runs their login shell.
// Tests that need any of these put in fakes of their own.
func TestMain(m *testing.M) {
	launchctlCommand = func(...string) ([]byte, error) {
		return nil, errors.New("tests never reach the real launchctl")
	}
	findTerminalDaemon = func(string) int { return 0 }
	stopProcess = func(int) error { return errors.New("tests never signal a real process") }
	loginShellEnv = func(context.Context) ([]string, error) {
		return nil, errors.New("tests never run the developer's login shell")
	}
	os.Exit(tmuxtest.Run(m))
}
