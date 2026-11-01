package usage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func museCall(at time.Time, usage string, input, cached, output int) string {
	return `{"recorded_at":` + itoa(int(at.UnixMicro())) + `,"payload_type":"runtime.session","payload":{"kind":"run","event":{"kind":"goal_usage_attribution","record":{"usage_id":"` + usage +
		`","usage_family":"provider","quantity":{"unit":"tokens","reported":true,"input_tokens":` + itoa(input) + `,"output_tokens":` + itoa(output) +
		`,"cached_tokens":` + itoa(cached) + `,"reasoning_tokens":0}}}}}`
}

// Muse's calls are its goal_usage_attribution events, on the model the run
// was set up with. A helper's calls count under the session it helped, and
// a call two logs hold counts once.
func TestMuseCallsCountOnceUnderTheirSession(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	const session = "01a1943f-eca7-78c1-ad8b-52f437e0be7f"
	model := `{"recorded_at":1,"payload_type":"run.model.configured","payload":{"record":{"provider_id":"meta","model_id":"muse-spark-1.3"}}}`
	write(t, filepath.Join(root, "2026/10/31", session, "session.jsonl"),
		`{"payload_type":"runtime.session","payload":{"event":{"kind":"model_request_configured","base_instructions":"…tokens…"}}}`,
		model, museCall(now, "usage-1", 30321, 0, 126), museCall(now, "usage-2", 21285, 1000, 601))
	write(t, filepath.Join(root, "2026/10/31", session, "subagent/01a1955b-04de-77e2-a67c-eef854a212ab/session.jsonl"),
		model, museCall(now, "usage-2", 21285, 1000, 601), museCall(now, "usage-3", 3598, 0, 138))

	m := newMuseReader()
	records, skipped := m.read(root, now.Add(-time.Hour))
	if skipped != 0 || len(records) != 4 {
		t.Fatalf("records = %+v, skipped %d", records, skipped)
	}
	for _, r := range records {
		if r.Agent != protocol.KindMuse || r.Session != session || r.Model != "muse-spark-1.3" || r.Provider != "meta" || r.Key == "" {
			t.Fatalf("record = %+v", r)
		}
	}
	catalog := Catalog{"meta": {"muse-spark-1.3": {Input: 1, Output: 2, CacheRead: 0.5}}}
	from, unit := Window(1, now)
	u := Summarize(records, catalog, 1, from, unit, now, 0)
	if len(u.Agents) != 1 || u.Agents[0].Sessions != 1 ||
		u.Tokens != (protocol.Tokens{Input: 30321 + 20285 + 3598, CacheRead: 1000, Output: 126 + 601 + 138}) {
		t.Fatalf("summary = %+v", u)
	}
	// Read again unchanged, the logs aren't parsed again; gone, they are let go.
	if again, _ := m.read(root, now.Add(-time.Hour)); len(again) != 4 {
		t.Fatalf("again = %d records", len(again))
	}
}

// Kiro counts no tokens; each turn says what it cost in credits, which reach
// the agent, its model, the day and the total.
func TestKiroTurnsCountTheirCredits(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	turn := func(at time.Time, credits ...string) string {
		metering := ""
		for i, c := range credits {
			if i > 0 {
				metering += ","
			}
			metering += `{"value":` + c + `,"unit":"credit","unitPlural":"credits"}`
		}
		return `{"end_timestamp":"` + at.Format(time.RFC3339Nano) + `","model":"auto","input_token_count":0,"output_token_count":0,` +
			`"cache_read_input_token_count":0,"cache_write_input_token_count":0,"metering_usage":[` + metering + `]}`
	}
	write(t, filepath.Join(dir, "9afec73d-0bce-4d68-91ff-95cfe36d542f.json"),
		`{"session_id":"9afec73d-0bce-4d68-91ff-95cfe36d542f","session_state":{"conversation_metadata":{"user_turn_metadatas":[`+
			turn(now.Add(-2*time.Minute), "0.25")+`,`+turn(now.Add(-time.Minute), "0.5", "0.25")+`,`+turn(now.Add(-48*time.Hour), "9")+`]}}}`)
	write(t, filepath.Join(dir, "broken.json"), `{"session_state":`)
	write(t, filepath.Join(dir, "9afec73d-0bce-4d68-91ff-95cfe36d542f.jsonl"), `{"version":"v1"}`)

	records, skipped := newKiroReader().read(dir, now.Add(-time.Hour))
	if skipped != 1 || len(records) != 2 {
		t.Fatalf("records = %+v, skipped %d", records, skipped)
	}
	from, unit := Window(1, now)
	u := Summarize(records, Catalog{}, 1, from, unit, now, skipped)
	if u.Credits != 1 || len(u.Agents) != 1 || u.Agents[0].Kind != protocol.KindKiro || u.Agents[0].Credits != 1 ||
		u.Agents[0].Sessions != 1 || u.Tokens.Total() != 0 || u.Cost.Total() != 0 {
		t.Fatalf("summary = %+v", u)
	}
	if len(u.Models) != 1 || u.Models[0].Model != "auto" || u.Models[0].Credits != 1 || u.Models[0].Priced {
		t.Fatalf("models = %+v", u.Models)
	}
	var inBuckets float64
	for _, b := range u.Buckets {
		inBuckets += b.Credits[protocol.KindKiro]
	}
	if inBuckets != 1 {
		t.Fatalf("buckets hold %v credits", inBuckets)
	}
}
