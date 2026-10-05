package source

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// A folder's history is cached for a few seconds, and every folder opened
// added an entry that was replaced but never removed. Expired entries go.
func TestOldFolderListingsAreDropped(t *testing.T) {
	r := NewRegistry()
	r.pastByDir = map[string]pastListing{}
	stale := time.Now().Add(-2 * pastListingTTL)
	for i := range 100 {
		r.pastByDir[fmt.Sprintf("/old/%d\x00200", i)] = pastListing{builtAt: stale}
	}
	if _, failures := r.pastIn(context.Background(), "/new", 200); len(failures) != 0 {
		t.Fatal(failures)
	}
	if got := len(r.pastByDir); got != 1 {
		t.Errorf("%d listings cached, want only the fresh one", got)
	}
}

// Every past session an adapter lists is remembered so its transcript can be
// opened, and the daemon runs for weeks. The oldest go past a bound.
func TestRememberedPastSessionsAreBounded(t *testing.T) {
	var past pastSessions
	for i := range maxPastSessions + 100 {
		past.remember(fmt.Sprintf("claude:%d", i), fmt.Sprintf("/t/%d.jsonl", i))
	}
	if got := len(past.paths); got > maxPastSessions {
		t.Errorf("%d past sessions remembered, want at most %d", got, maxPastSessions)
	}
	if _, ok := past.path(fmt.Sprintf("claude:%d", maxPastSessions+99)); !ok {
		t.Error("the newest was forgotten")
	}
	// Listing a session again keeps it.
	past.remember("claude:150", "/t/150.jsonl")
	for i := range 50 {
		past.remember(fmt.Sprintf("codex:%d", i), "/x")
	}
	if _, ok := past.path("claude:150"); !ok {
		t.Error("a session listed again was evicted as if it were old")
	}
}
