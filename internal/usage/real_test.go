package usage

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// TestThisMacsRecords reads this Mac's own records, read only, when
// AGENTMAN_USAGE_REAL is set: what the scanner keeps once it has read all of
// them, and how long a first read and a second take. Two runs compare alike
// with AGENTMAN_USAGE_CATALOG naming a models.dev catalog to price with and
// AGENTMAN_USAGE_NOW (RFC 3339) fixing the time; AGENTMAN_USAGE_DUMP names a
// file to write every range's summary to.
func TestThisMacsRecords(t *testing.T) {
	if os.Getenv("AGENTMAN_USAGE_REAL") == "" {
		t.Skip("reads this Mac's records: set AGENTMAN_USAGE_REAL=1")
	}
	ctx := context.Background()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	var catalog Catalog
	if path := os.Getenv("AGENTMAN_USAGE_CATALOG"); path != "" {
		catalog = readCatalog(path)
	} else {
		catalog = NewPrices(t.TempDir()).Catalog(ctx)
	}
	if len(catalog) == 0 {
		t.Fatal("no prices")
	}
	now := time.Now()
	if set := os.Getenv("AGENTMAN_USAGE_NOW"); set != "" {
		if now, err = time.Parse(time.RFC3339Nano, set); err != nil {
			t.Fatal(err)
		}
		now = now.In(time.Local)
	}

	heap := func() (alloc, inUse float64) {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return float64(m.HeapAlloc) / (1 << 20), float64(m.HeapInuse) / (1 << 20)
	}
	baseAlloc, baseInUse := heap()
	s := NewScanner(home, WithCatalog(catalog))
	start := time.Now()
	all := s.Summary(ctx, 0, now)
	first := time.Since(start)
	alloc, inUse := heap()
	start = time.Now()
	s.Summary(ctx, 0, now)
	again := time.Since(start)
	t.Logf("all of it: %d sessions, %d tokens, $%.2f, %d skipped", all.Sessions, all.Tokens.Total(), all.Cost.Total(), all.Skipped)
	t.Logf("first read %v, again %v; the scanner keeps %.1f MB live (%.1f MB of heap in use)",
		first.Round(time.Millisecond), again.Round(time.Millisecond), alloc-baseAlloc, inUse-baseInUse)

	if path := os.Getenv("AGENTMAN_USAGE_DUMP"); path != "" {
		out := map[string]protocol.Usage{}
		for _, days := range []int{1, 7, 30, 90, 180, 365, 0} {
			out[strconv.Itoa(days)] = s.Summary(ctx, days, now)
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runtime.KeepAlive(s)
}
