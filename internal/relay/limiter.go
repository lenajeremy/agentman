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

// allow records a hit for key and reports whether it is within the limit.
func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, at := range l.hits[key] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}

	if len(kept) >= l.limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)

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
	return true
}
