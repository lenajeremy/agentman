package relay

import (
	"sync"
	"time"
)

// limiter is a sliding-window counter used to bound failed pairing attempts.
//
// Brute force against a typed pairing code has to be stopped somewhere, and
// the only honest place is at the caller who made the guesses. Charging it
// anywhere shared — every outstanding code, or a group of accounts — makes
// one attacker's noise into everybody else's problem.
type limiter struct {
	mu     sync.Mutex
	window time.Duration
	limit  int
	hits   map[string][]time.Time
	active map[string]int
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{
		window: window,
		limit:  limit,
		hits:   map[string][]time.Time{},
		active: map[string]int{},
	}
}

// allow atomically spends one request from a key's sliding-window budget.
// Keeping the check and record under one lock matters: otherwise a burst of
// concurrent requests can all observe the old count and bypass the limit.
func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.liveLocked(key, now))+l.active[key] >= l.limit {
		return false
	}
	l.hits[key] = append(l.hits[key], now)
	l.sweepLocked(now)
	return true
}

// reserve temporarily occupies one slot without charging it yet. Pairing-code
// redemption uses this so a valid code is not counted as a failed guess while
// many concurrent guesses still cannot race through one apparent slot.
func (l *limiter) reserve(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.liveLocked(key, now))+l.active[key] >= l.limit {
		return false
	}
	l.active[key]++
	return true

}

// release retires a reservation, charging it when the attempted operation
// failed. Callers pass failed=false for a valid pairing code.
func (l *limiter) release(key string, now time.Time, failed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active[key] > 1 {
		l.active[key]--
	} else {
		delete(l.active, key)
	}
	if failed {
		l.hits[key] = append(l.liveLocked(key, now), now)
	}
	l.sweepLocked(now)
}

// liveLocked returns the hits still inside the window, filtering in place.
func (l *limiter) liveLocked(key string, now time.Time) []time.Time {
	cutoff := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, at := range l.hits[key] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	l.hits[key] = kept
	return kept
}

func (l *limiter) sweepLocked(now time.Time) {
	cutoff := now.Add(-l.window)

	// Opportunistic sweep: without it, a long-lived relay accumulates one
	// slice per distinct key forever, which an attacker could grow on purpose
	// by rotating the forwarded-for header.
	if len(l.hits) > 4096 {
		for k, times := range l.hits {
			if len(times) == 0 || times[len(times)-1].Before(cutoff) {
				delete(l.hits, k)
			}
		}
	}
}
