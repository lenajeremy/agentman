package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/limits"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// Limits are asked for with no arguments and answered with each agent's
// plan; with no Codex daemon connected, Codex's are read from its rollouts.
func TestLimitsAreReadFromTheAgents(t *testing.T) {
	// No claude on the PATH: Claude Code is left out rather than started.
	t.Setenv("PATH", t.TempDir())
	home := t.TempDir()
	at := time.Now().Add(-10 * time.Minute).UTC()
	resets := time.Now().Add(time.Hour).Unix()
	line := `{"timestamp":"` + at.Format(time.RFC3339Nano) + `","type":"event_msg","payload":{"type":"token_count",` +
		`"rate_limits":{"limit_id":"codex","plan_type":"plus","primary":{"used_percent":42.0,"window_minutes":300,"resets_at":` +
		strconv.FormatInt(resets, 10) + `},"secondary":null}}}`
	path := filepath.Join(home, ".codex", "sessions", "2026", "11", "01", "rollout-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, _ := agentWithSource(t, &streamingSource{})
	d.limits = limits.NewReader(home, limits.WithCodexDaemon(d.codexLimits))

	event := d.Handle(context.Background(), protocol.Request{Type: protocol.ReqLimits, Force: true})
	if event.Type != protocol.EvtLimits || event.Limits == nil {
		t.Fatalf("event = %+v", event)
	}
	if len(event.Limits.Agents) != 1 {
		t.Fatalf("agents = %+v", event.Limits.Agents)
	}
	codex := event.Limits.Agents[0]
	if codex.Kind != protocol.KindCodex || codex.Plan != "Plus" || !codex.LastTurn || len(codex.Windows) != 1 ||
		codex.Windows[0].Used != 42 || codex.Windows[0].ResetsAt != resets*1000 {
		t.Errorf("codex = %+v", codex)
	}

	// The answer travels with the agents' list even when it is empty.
	empty := limits.NewReader(t.TempDir())
	d.limits = empty
	data, err := json.Marshal(d.Handle(context.Background(), protocol.Request{Type: protocol.ReqLimits}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"limits":{"agents":[]}`) {
		t.Errorf("empty answer = %s", data)
	}
}
