package daemon

import (
	"context"
	"os"
	"time"

	"github.com/lenajeremy/agentman/internal/limits"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// codexLimitsTimeout bounds asking Codex's shared daemon, which asks OpenAI.
const codexLimitsTimeout = 5 * time.Second

// planLimits answers ReqLimits: what is left of each agent's plan, as each
// agent reports it.
func (d *Daemon) planLimits(ctx context.Context, req protocol.Request) protocol.Event {
	answer := d.limitsReader().Read(ctx, req.Force)
	return protocol.Event{Type: protocol.EvtLimits, Limits: &answer}
}

// limitsReader is the one reader of the agents' limits, made when first
// needed; it keeps its answer for a couple of minutes.
func (d *Daemon) limitsReader() *limits.Reader {
	d.limitsOnce.Do(func() {
		if d.limits == nil {
			home, _ := os.UserHomeDir()
			d.limits = limits.NewReader(home, limits.WithCodexDaemon(d.codexLimits))
		}
	})
	return d.limits
}

// codexLimits asks Codex's shared daemon, through the connection the Codex
// adapter keeps to it.
func (d *Daemon) codexLimits(ctx context.Context) (limits.CodexSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, codexLimitsTimeout)
	defer cancel()
	read, err := d.registry.CodexRateLimits(ctx)
	if err != nil {
		return limits.CodexSnapshot{}, err
	}
	// Another product's bucket says nothing of Codex's; its rollouts will.
	if read.LimitID != "" && read.LimitID != "codex" {
		return limits.CodexSnapshot{}, nil
	}
	window := func(w *source.CodexRateWindow) *limits.CodexWindow {
		if w == nil {
			return nil
		}
		out := &limits.CodexWindow{Used: w.UsedPercent, Minutes: w.WindowDurationMins}
		if w.ResetsAt > 0 {
			out.ResetsAt = time.Unix(w.ResetsAt, 0)
		}
		return out
	}
	return limits.CodexSnapshot{
		Plan: read.PlanType, Primary: window(read.Primary), Secondary: window(read.Secondary), At: time.Now(),
	}, nil
}
