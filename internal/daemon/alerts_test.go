package daemon

import (
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/source"
)

func alertingDaemon(t *testing.T) *Daemon {
	t.Helper()
	return New(source.NewRegistry(), &recordingSink{})
}

// A turn long enough to have walked away from, on a session nobody is looking
// at, is exactly what push exists for.
func TestALongTurnOnAnUnwatchedSessionIsAnnounced(t *testing.T) {
	d := alertingDaemon(t)
	now := time.Now()
	d.turns["claude:s1"] = turnState{startedAt: now.Add(-5 * time.Minute)}

	if reason := d.suppressTurnAlert("claude:s1", now); reason != "" {
		t.Fatalf("suppressed: %s", reason)
	}
}

// The rule the code always claimed and never checked: a phone with the session
// open already has the completion on screen.
func TestAWatchedSessionIsNotAnnounced(t *testing.T) {
	d := alertingDaemon(t)
	now := time.Now()
	d.turns["claude:s1"] = turnState{startedAt: now.Add(-5 * time.Minute)}
	d.follows["claude:s1"] = &follow{subscribers: map[string]struct{}{"device-1": {}}}

	if reason := d.suppressTurnAlert("claude:s1", now); reason == "" {
		t.Fatal("announced a session the user is watching")
	}
}

// A subscriber leaving is what makes push relevant again, so the suppression
// must lift with it rather than latching.
func TestAnnouncementResumesWhenTheLastWatcherLeaves(t *testing.T) {
	d := alertingDaemon(t)
	now := time.Now()
	d.turns["claude:s1"] = turnState{startedAt: now.Add(-5 * time.Minute)}
	d.follows["claude:s1"] = &follow{subscribers: map[string]struct{}{"device-1": {}}}
	d.suppressTurnAlert("claude:s1", now)

	d.follows["claude:s1"] = &follow{subscribers: map[string]struct{}{}}
	if reason := d.suppressTurnAlert("claude:s1", now); reason != "" {
		t.Fatalf("still suppressed after the watcher left: %s", reason)
	}
}

func TestAShortTurnIsNotAnnounced(t *testing.T) {
	d := alertingDaemon(t)
	now := time.Now()
	d.turns["claude:s1"] = turnState{startedAt: now.Add(-2 * time.Second)}

	if reason := d.suppressTurnAlert("claude:s1", now); reason == "" {
		t.Fatal("announced a two-second turn")
	}
}

// Several turns in a row are one event to a person.
func TestABurstOfTurnsIsAnnouncedOnce(t *testing.T) {
	d := alertingDaemon(t)
	start := time.Now()
	long := func(at time.Time) { d.turns["claude:s1"] = turnState{startedAt: at.Add(-5 * time.Minute)} }

	long(start)
	if reason := d.suppressTurnAlert("claude:s1", start); reason != "" {
		t.Fatalf("first: %s", reason)
	}
	for i := 1; i <= 3; i++ {
		at := start.Add(time.Duration(i) * 20 * time.Second)
		long(at)
		if reason := d.suppressTurnAlert("claude:s1", at); reason == "" {
			t.Errorf("announcement %d inside the coalesce window was not held", i)
		}
	}
	// Past the window, the next completion is news again.
	at := start.Add(alertCoalesce + time.Second)
	long(at)
	if reason := d.suppressTurnAlert("claude:s1", at); reason != "" {
		t.Fatalf("held past the window: %s", reason)
	}
}

// Coalescing is per session: one chatty agent must not silence another.
func TestCoalescingDoesNotCrossSessions(t *testing.T) {
	d := alertingDaemon(t)
	now := time.Now()
	d.turns["claude:s1"] = turnState{startedAt: now.Add(-5 * time.Minute)}
	d.turns["codex:s2"] = turnState{startedAt: now.Add(-5 * time.Minute)}

	if reason := d.suppressTurnAlert("claude:s1", now); reason != "" {
		t.Fatalf("first session: %s", reason)
	}
	if reason := d.suppressTurnAlert("codex:s2", now); reason != "" {
		t.Fatalf("a second session was silenced by the first: %s", reason)
	}
}

// A session with no recorded start is not evidence of a short turn, so it is
// announced rather than swallowed.
func TestAnUnknownTurnLengthIsStillAnnounced(t *testing.T) {
	d := alertingDaemon(t)
	if reason := d.suppressTurnAlert("claude:never-seen", time.Now()); reason != "" {
		t.Fatalf("suppressed a turn of unknown length: %s", reason)
	}
}

// startTurnLocked is the only place a turn begins, so it is the only place the
// clock can start.
func TestStartingATurnRecordsWhen(t *testing.T) {
	d := alertingDaemon(t)
	before := time.Now()
	d.mu.Lock()
	d.startTurnLocked("claude:s1")
	d.mu.Unlock()

	if got := d.turns["claude:s1"].startedAt; got.Before(before) || got.After(time.Now()) {
		t.Fatalf("startedAt = %v, outside the window it was called in", got)
	}
}
