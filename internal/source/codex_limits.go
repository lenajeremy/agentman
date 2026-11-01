package source

import (
	"context"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// CodexRateLimits is what Codex's shared daemon says is left of the ChatGPT
// plan Codex is signed in with: the windows its /status shows. Codex reads
// them from OpenAI with its own sign-in; Agentman only asks Codex.
type CodexRateLimits struct {
	// LimitID names the metered bucket: "codex" for Codex's own.
	LimitID string `json:"limitId"`
	// PlanType is the plan: "plus", "pro", "team" and so on.
	PlanType string `json:"planType"`
	// Primary and Secondary are the windows by position, the five hours and
	// the week on a paid plan; nil when the plan has no such window.
	Primary   *CodexRateWindow `json:"primary"`
	Secondary *CodexRateWindow `json:"secondary"`
}

// CodexRateWindow is one window of a plan.
type CodexRateWindow struct {
	UsedPercent float64 `json:"usedPercent"`
	// WindowDurationMins is how long the window lasts; 0 when not said.
	WindowDurationMins int `json:"windowDurationMins"`
	// ResetsAt is when it resets, in seconds since 1970; 0 when not said.
	ResetsAt int64 `json:"resetsAt"`
}

// CodexRateLimits asks Codex's shared daemon what is left of the plan:
// ErrNoCodexDaemon when there is no connection to it, and the caller reads
// Codex's own records instead.
func (r *Registry) CodexRateLimits(ctx context.Context) (CodexRateLimits, error) {
	r.mu.RLock()
	s := r.sources[protocol.KindCodex]
	r.mu.RUnlock()
	if reader, ok := s.(interface {
		RateLimits(context.Context) (CodexRateLimits, error)
	}); ok {
		return reader.RateLimits(ctx)
	}
	return CodexRateLimits{}, ErrNoCodexDaemon
}

// RateLimits asks the shared daemon for the account's rate limits.
//
// The answer carries the historical single bucket and, from newer Codex, the
// same keyed by metered bucket; Codex's own is "codex", which is preferred
// when it is there, as the single one may be another product's.
func (s *CodexSource) RateLimits(ctx context.Context) (CodexRateLimits, error) {
	server := s.connection()
	if server == nil {
		return CodexRateLimits{}, ErrNoCodexDaemon
	}
	var reply struct {
		RateLimits CodexRateLimits            `json:"rateLimits"`
		ByLimitID  map[string]CodexRateLimits `json:"rateLimitsByLimitId"`
	}
	if err := server.call(ctx, "account/rateLimits/read", nil, &reply); err != nil {
		if codexFallBack(err) {
			return CodexRateLimits{}, ErrNoCodexDaemon
		}
		return CodexRateLimits{}, err
	}
	if limits, ok := reply.ByLimitID["codex"]; ok {
		if limits.LimitID == "" {
			limits.LimitID = "codex"
		}
		return limits, nil
	}
	return reply.RateLimits, nil
}
