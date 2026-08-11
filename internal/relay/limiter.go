package relay

import (
	"sync"
	"time"
)

// limiter is a sliding-window counter used to bound failed pairing attempts.
//
// Brute force against a six-digit code has to be stopped somewhere, and the
// only honest place is at the source of the guesses. The first version of this
// charged every failed guess against every outstanding pairing code, which
// looked like rate limiting but was actually a cross-account denial of
// service: on a shared relay, ten wrong guesses from a stranger deleted every
// other user's in-flight code. A wrong guess cannot be attributed to a code —
// that is exactly what makes it wrong — so it is attributed to the caller
// instead, and nobody else's pairing is affected.
type limiter struct {
	mu     sync.Mutex
	window time.Duration
	limit  int
	hits   map[string][]time.Time
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{window: window, limit: limit, hits: map[string][]time.Time{}}
}

// over reports whether a key has already exhausted its budget, without
// recording anything.
//
// Checking and recording are separate so that only failures are charged: a
// correct code is honoured without spending anyone's budget, which is what
// keeps a flood of wrong guesses from blocking real pairings.
func (l *limiter) over(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.liveLocked(key, time.Now())) >= l.limit
}

// record adds a hit for key.
func (l *limiter) record(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits[key] = append(l.liveLocked(key, now), now)
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
