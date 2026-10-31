package daemon

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/usage"
)

// usageDays are the ranges ReqUsage takes; 0 is all of it.
var usageDays = map[int]bool{0: true, 1: true, 7: true, 30: true, 90: true, 180: true, 365: true}

func validateUsage(req protocol.Request) error {
	if !usageDays[req.Days] {
		return fmt.Errorf("daemon: usage covers 1, 7, 30, 90, 180 or 365 days, or 0 for all of it")
	}
	return nil
}

// usageSummary answers ReqUsage: what the agents on this Mac spent over the
// last days, from their own records.
func (d *Daemon) usageSummary(ctx context.Context, req protocol.Request) protocol.Event {
	summary := d.usageScanner().Summary(ctx, req.Days, time.Now())
	return protocol.Event{Type: protocol.EvtUsage, Usage: &summary}
}

// usageScanner is the one reader of the agents' records, made when first
// needed; it keeps what it read, so later asks read only what was added.
func (d *Daemon) usageScanner() *usage.Scanner {
	d.usageOnce.Do(func() {
		if d.usage == nil {
			home, _ := os.UserHomeDir()
			d.usage = usage.NewScanner(home, usage.NewPrices(home))
		}
	})
	return d.usage
}

// warmUsage reads the last 90 days once, soon after the daemon starts and
// out of the way of everything else, so the first ask for usage is quick.
func (d *Daemon) warmUsage(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}
	d.usageScanner().Summary(ctx, 90, time.Now())
}
