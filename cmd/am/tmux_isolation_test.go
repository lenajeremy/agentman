package main

import (
	"os"
	"testing"

	"github.com/lenajeremy/agentman/internal/tmux/tmuxtest"
)

// Every tmux command these tests reach runs on a private server, never the
// one the developer is working in. See tmuxtest.
func TestMain(m *testing.M) { os.Exit(tmuxtest.Run(m)) }
