package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/source"
)

// A slow sweep is written to the log, and a failure when it is new, but not
// every second while it lasts.
func TestASlowOrFailingSweepIsLoggedOnce(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	previous := Logf
	Logf = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, format)
	}
	defer func() { Logf = previous }()
	agent := New(source.NewRegistry(), &recordingSink{})

	agent.noteSweep(time.Second, nil)
	agent.noteSweep(8*time.Second, nil)
	agent.noteSweep(9*time.Second, nil)
	if len(lines) != 1 || !strings.Contains(lines[0], "sweep took") {
		t.Fatalf("logged %q, want the slow sweep once", lines)
	}

	// A minute on, a failure is new; the same one again is not.
	agent.sweepLogged = time.Now().Add(-2 * time.Minute)
	failed := context.DeadlineExceeded
	agent.noteSweep(time.Second, failed)
	agent.sweepLogged = time.Now().Add(-2 * time.Minute)
	agent.noteSweep(time.Second, failed)
	if len(lines) != 2 {
		t.Fatalf("logged %q, want one line for the failure", lines)
	}
}
