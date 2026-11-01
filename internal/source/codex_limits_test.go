package source

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The plan's limits are asked of the shared daemon, Codex's own bucket
// preferred over the historical single one.
func TestCodexRateLimitsAreAskedOfTheSharedDaemon(t *testing.T) {
	fake := startFakeCodexDaemon(t)
	server := newTestCodexAppServer(t, fake.socket)
	runCodexAppServer(t, server)
	conn := fake.accept(t)
	conn.handshake(t)
	waitConnected(t, server)
	codex := &CodexSource{appServer: server}

	type answer struct {
		limits CodexRateLimits
		err    error
	}
	result := make(chan answer, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		limits, err := codex.RateLimits(ctx)
		result <- answer{limits, err}
	}()
	request := conn.expect(t, "account/rateLimits/read")
	conn.reply(t, request, map[string]any{
		"rateLimits": map[string]any{"limitId": "premium", "primary": nil, "secondary": nil},
		"rateLimitsByLimitId": map[string]any{
			"codex": map[string]any{
				"planType":  "plus",
				"primary":   map[string]any{"usedPercent": 95, "windowDurationMins": 300, "resetsAt": 1793523329},
				"secondary": map[string]any{"usedPercent": 84, "windowDurationMins": 10080, "resetsAt": 1793779301},
			},
		},
	})
	var got answer
	select {
	case got = <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("never answered")
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	limits := got.limits
	if limits.LimitID != "codex" || limits.PlanType != "plus" || limits.Primary == nil || limits.Secondary == nil {
		t.Fatalf("limits = %+v", limits)
	}
	if limits.Primary.UsedPercent != 95 || limits.Primary.WindowDurationMins != 300 || limits.Secondary.ResetsAt != 1793779301 {
		t.Errorf("windows = %+v, %+v", *limits.Primary, *limits.Secondary)
	}
}

// Without a connection the caller is told to read Codex's records instead.
func TestCodexRateLimitsWithoutTheDaemon(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.CodexRateLimits(context.Background()); !errors.Is(err, ErrNoCodexDaemon) {
		t.Errorf("no Codex adapter: err = %v", err)
	}
	registry.Add(&CodexSource{})
	if _, err := registry.CodexRateLimits(context.Background()); !errors.Is(err, ErrNoCodexDaemon) {
		t.Errorf("never connected: err = %v", err)
	}
}
