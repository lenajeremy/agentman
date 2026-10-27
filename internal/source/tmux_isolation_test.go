package source

import (
	"os"
	"testing"

	"github.com/lenajeremy/agentman/internal/tmux/tmuxtest"
)

// Every tmux command these tests reach runs on a private server, never the
// one the developer is working in. See tmuxtest. And OpenCode 2's service is
// looked for in an empty folder, never the developer's own.
func TestMain(m *testing.M) {
	state, err := os.MkdirTemp("", "agentman-state-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_STATE_HOME", state)
	code := tmuxtest.Run(m)
	os.RemoveAll(state)
	os.Exit(code)
}
