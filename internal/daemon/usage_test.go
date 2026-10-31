package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/usage"
)

// Usage is asked for by days, and answered from the agents' own records.
func TestUsageIsSummedFromTheAgentsRecords(t *testing.T) {
	home := t.TempDir()
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	line := `{"type":"assistant","timestamp":"` + at + `","sessionId":"s1","requestId":"r1","message":{"id":"m1","model":"claude-opus-5-5",` +
		`"usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":100}}}`
	path := filepath.Join(home, ".claude", "projects", "p", "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, _ := agentWithSource(t, &streamingSource{})
	d.usage = usage.NewScanner(home, usage.WithCatalog(usage.Catalog{
		"anthropic": {"claude-opus-5-5": {Input: 4, Output: 20}},
	}))

	event := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqUsage, Days: 7})
	if event.Type != protocol.EvtUsage || event.Usage == nil {
		t.Fatalf("event = %+v", event)
	}
	u := event.Usage
	if u.Days != 7 || len(u.Buckets) != 7 || u.Tokens.Total() != 1100 || u.Cost.Total() != (1000*4+100*20)/1e6 {
		t.Errorf("usage = %+v", u)
	}

	bad := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqUsage, Days: 3})
	if bad.Type != protocol.EvtError || !strings.Contains(bad.Error, "1, 7, 30, 90, 180 or 365") {
		t.Errorf("3 days = %+v", bad)
	}
}
