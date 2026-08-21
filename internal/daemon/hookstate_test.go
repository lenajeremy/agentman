package daemon

import (
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

func waitingDaemon(t *testing.T, at int64, until time.Duration) *Daemon {
	t.Helper()
	d := New(source.NewRegistry(), nil)
	d.hookStates["claude:s1"] = hookState{
		state: protocol.StateWaitingInput,
		at:    at,
		until: time.Now().Add(until),
	}
	return d
}

// Claude fires Notification both when it opens a permission prompt and when the
// prompt has merely sat idle. For a tmux session the pane settles which it was:
// discovery parses any real prompt, so no question means the agent is not
// blocked, and reporting "needs you" gives the user nothing to answer.
func TestIdleNotificationDoesNotPinATmuxSessionToNeedsYou(t *testing.T) {
	d := waitingDaemon(t, time.Now().UnixMilli(), hookWaitingGrace)

	got := d.applyHookStateLocked(protocol.Session{
		ID:     "claude:s1",
		State:  protocol.StateIdle,
		Inject: protocol.InjectTmux,
	}, time.Now())

	if got.State != protocol.StateIdle {
		t.Fatalf("state = %q, want idle — a pane with no question is not blocked", got.State)
	}
	if _, kept := d.hookStates["claude:s1"]; kept {
		t.Fatal("the lease survived, so the next sweep would report waiting again")
	}
}

// A real prompt still wins: discovery found a question, so the session is
// genuinely blocked regardless of what any hook said.
func TestAParsedQuestionStillReportsNeedsYou(t *testing.T) {
	d := waitingDaemon(t, time.Now().UnixMilli(), hookWaitingGrace)

	got := d.applyHookStateLocked(protocol.Session{
		ID:       "claude:s1",
		State:    protocol.StateWaitingInput,
		Inject:   protocol.InjectTmux,
		Question: &protocol.Question{ID: "q1", Prompt: "Allow?"},
	}, time.Now())

	if got.State != protocol.StateWaitingInput {
		t.Fatalf("state = %q, want waiting_input", got.State)
	}
	if _, kept := d.hookStates["claude:s1"]; !kept {
		t.Fatal("dropped the lease while the question is still on screen")
	}
}

// Sessions we cannot see into have only the hook to go on, so the lease is
// believed — but not forever.
func TestAWaitingLeaseExpiresForASessionWeCannotInspect(t *testing.T) {
	now := time.Now()
	d := waitingDaemon(t, now.UnixMilli(), hookWaitingGrace)
	session := protocol.Session{
		ID:     "claude:s1",
		State:  protocol.StateIdle,
		Inject: protocol.InjectHook,
	}

	if got := d.applyHookStateLocked(session, now); got.State != protocol.StateWaitingInput {
		t.Fatalf("state = %q, want waiting_input while the lease is live", got.State)
	}

	if got := d.applyHookStateLocked(session, now.Add(hookWaitingGrace+time.Minute)); got.State != protocol.StateIdle {
		t.Fatalf("state = %q, want idle once the lease has expired", got.State)
	}
	if _, kept := d.hookStates["claude:s1"]; kept {
		t.Fatal("an expired lease was retained")
	}
}
