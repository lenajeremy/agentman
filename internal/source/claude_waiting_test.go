package source

import (
	"context"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Claude Code's registry says when a session is blocked on someone:
// status "waiting", with what it waits for ("dialog open", "input needed"…).
// It was read as idle, so a session started in an ordinary terminal and
// stopped on a permission prompt looked finished on the phone.
func TestAClaudeSessionWaitingOnSomeoneShowsAsWaiting(t *testing.T) {
	home := fakeClaudeHome(t, "/Users/me/work/proj", "sess-wait", "waiting")
	src := isolatedClaudeSource(t, home)
	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].State != protocol.StateWaitingInput {
		t.Fatalf("a waiting registry entry was reported as %+v", sessions)
	}
}

func TestClaudeRegistryStatusesMapToStates(t *testing.T) {
	for status, want := range map[string]protocol.State{
		"busy":    protocol.StateBusy,
		"idle":    protocol.StateIdle,
		"waiting": protocol.StateWaitingInput,
		"":        protocol.StateIdle, // older CLIs omit it
		"shell":   protocol.StateIdle, // meaning unknown: not worth a false alarm
	} {
		if got := claudeState(status); got != want {
			t.Errorf("claudeState(%q) = %s, want %s", status, got, want)
		}
	}
}

// One conversation open in two processes reports the most actionable state
// either has: blocked on the user beats working.
func TestMergedClaudeProcessesReportWaitingOverBusy(t *testing.T) {
	for _, order := range [][2]string{{"busy", "waiting"}, {"waiting", "busy"}, {"idle", "waiting"}} {
		merged := mergeClaudeCandidates([]claudeCandidate{
			{file: claudeSessionFile{PID: 1, SessionID: "s", Status: order[0]}},
			{file: claudeSessionFile{PID: 2, SessionID: "s", Status: order[1]}},
		})
		if len(merged) != 1 || merged[0].file.Status != "waiting" {
			t.Errorf("%v merged to %+v", order, merged)
		}
	}
}
