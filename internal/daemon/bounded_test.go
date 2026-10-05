package daemon

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// The daemon runs for weeks. What it remembers per session has to end with
// the session, or stay bounded, however many sessions come and go.

func TestSeenFilesAreKeptForRecentSessionsOnly(t *testing.T) {
	seen := newSeenPaths()
	for i := range maxSeenSessions + 50 {
		seen.record(fmt.Sprintf("claude:s%d", i), []protocol.Message{toolMessage(fmt.Sprintf("/tmp/s%d.png", i))})
	}
	if got := len(seen.sessions); got > maxSeenSessions {
		t.Errorf("remembering files for %d sessions, want at most %d", got, maxSeenSessions)
	}
	newest := fmt.Sprintf("claude:s%d", maxSeenSessions+49)
	if !seen.allows(newest, fmt.Sprintf("/tmp/s%d.png", maxSeenSessions+49)) {
		t.Error("the newest session's files were forgotten")
	}
	// Looking at a session's files again keeps them.
	seen.record("claude:s60", []protocol.Message{toolMessage("/tmp/again.png")})
	for i := range 10 {
		seen.record(fmt.Sprintf("claude:later%d", i), []protocol.Message{toolMessage("/tmp/x.png")})
	}
	if !seen.allows("claude:s60", "/tmp/again.png") {
		t.Error("a session read again was evicted as if it were old")
	}
}

func TestAnEndedSessionLeavesNoAlertOrLaunchState(t *testing.T) {
	bulk := &bulkDiscoverySource{sessions: []protocol.Session{{ID: "claude:s1", Kind: protocol.KindClaude}}}
	registry := source.NewRegistry()
	registry.Add(bulk)
	agent := New(registry, &recordingSink{})
	ctx := context.Background()
	agent.refresh(ctx, true)
	agent.mu.Lock()
	agent.lastAlert["claude:s1"] = time.Now()
	agent.launches["client-1"] = "claude:s1"
	agent.mu.Unlock()

	bulk.sessions = nil
	agent.refresh(ctx, false)

	agent.mu.Lock()
	defer agent.mu.Unlock()
	if _, kept := agent.lastAlert["claude:s1"]; kept {
		t.Error("an ended session's alert time was kept")
	}
	if _, kept := agent.launches["client-1"]; kept {
		t.Error("a launch of an ended session was kept")
	}
}

func TestLaunchesThatNeverAppearedAreNotKeptForever(t *testing.T) {
	agent := New(source.NewRegistry(), &recordingSink{})
	agent.refresh(context.Background(), true)
	agent.mu.Lock()
	for i := range maxRememberedLaunches + 10 {
		agent.launches[fmt.Sprintf("client-%d", i)] = fmt.Sprintf("claude:never-%d", i)
	}
	agent.mu.Unlock()
	agent.refresh(context.Background(), false)
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if got := len(agent.launches); got > maxRememberedLaunches {
		t.Errorf("%d launches remembered, want at most %d", got, maxRememberedLaunches)
	}
}
