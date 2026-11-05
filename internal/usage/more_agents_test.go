package usage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func museCall(at time.Time, usage string, input, cached, output int) string {
	return museOwnedCall(at, usage, "main-root", "main_root", input, cached, output)
}

// museOwnedCall is a call made by owner: the session's own run (main-root),
// or a helper by its id.
func museOwnedCall(at time.Time, usage, owner, ownerType string, input, cached, output int) string {
	return `{"recorded_at":` + itoa(int(at.UnixMicro())) + `,"payload_type":"runtime.session","payload":{"kind":"run","event":{"kind":"goal_usage_attribution","record":{"usage_id":"` + usage +
		`","usage_family":"provider","quantity":{"unit":"tokens","reported":true,"input_tokens":` + itoa(input) + `,"output_tokens":` + itoa(output) +
		`,"cached_tokens":` + itoa(cached) + `,"reasoning_tokens":42},"owner":{"requester_kind":"reminder","owner_id":"` + owner + `","owner_type":"` + ownerType + `"}}}}}`
}

func museModel(model string) string {
	return `{"recorded_at":1,"payload_type":"run.model.configured","payload":{"record":{"provider_id":"meta","model_id":"` + model + `"}}}`
}

// Muse's calls are its goal_usage_attribution events, on the model the run
// was set up with. A helper's calls count under the session it helped, and
// a call two logs hold counts once: the session's log writes its helpers'
// calls again under usage ids of its own, and those are known by the
// helper and the counts, and kept on the helper's own model.
func TestMuseCallsCountOnceUnderTheirSession(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	const session = "01a1943f-eca7-78c1-ad8b-52f437e0be7f"
	const helper = "01a1955b-04de-77e2-a67c-eef854a212ab"
	write(t, filepath.Join(root, "2026/10/31", session, "session.jsonl"),
		`{"payload_type":"runtime.session","payload":{"event":{"kind":"model_request_configured","base_instructions":"…tokens…"}}}`,
		museModel("muse-wonder-1.0"), museCall(now, "usage-1", 30321, 0, 126), museCall(now, "usage-2", 21285, 1000, 601),
		// The helper's call again, and one by a helper that kept no log.
		museOwnedCall(now, "usage-69f8", helper, "reminder_child", 3598, 0, 138),
		museOwnedCall(now, "usage-d8a7", "01a1955b-0517-7dd3-923e-2bcba5096666", "reminder_child", 3516, 0, 442))
	write(t, filepath.Join(root, "2026/10/31", session, "subagent", helper, "session.jsonl"),
		museModel("muse-spark-1.3"), museCall(now, "usage-2", 21285, 1000, 601),
		museOwnedCall(now, "usage-5723", helper, "reminder_child", 3598, 0, 138))

	m := newMuseReader()
	records, skipped := m.read(root, now.Add(-time.Hour))
	if skipped != 0 || len(records) != 4 {
		t.Fatalf("records = %+v, skipped %d", records, skipped)
	}
	models := map[string]string{}
	for _, r := range records {
		if r.Agent != protocol.KindMuse || r.Session != session || r.Provider != "meta" || r.Key == "" {
			t.Fatalf("record = %+v", r)
		}
		models[itoa(int(r.Tokens.Input))] = r.Model
	}
	if models["30321"] != "muse-wonder-1.0" || models["3598"] != "muse-spark-1.3" || models["3516"] != "muse-wonder-1.0" {
		t.Fatalf("models = %v", models)
	}
	catalog := Catalog{"meta": {"muse-spark-1.3": {Input: 1, Output: 2, CacheRead: 0.5}}}
	from, unit := Window(1, now)
	u := Summarize(records, catalog, 1, from, unit, now, 0)
	if len(u.Agents) != 1 || u.Agents[0].Sessions != 1 ||
		u.Tokens != (protocol.Tokens{Input: 30321 + 20285 + 3598 + 3516, CacheRead: 1000, Output: 126 + 601 + 138 + 442}) {
		t.Fatalf("summary = %+v", u)
	}
	// Read again unchanged, the logs aren't parsed again; gone, they are let go.
	if again, _ := m.read(root, now.Add(-time.Hour)); len(again) != 4 {
		t.Fatalf("again = %d records", len(again))
	}
}

// Muse's automated reviewer asks a model before a step that needs approval:
// that call is on the review's event, on the reviewer's own model, and
// counts with the session's.
func TestMuseReviewsCountTheirCall(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	const session = "01a19c42-9815-79b0-9e14-ddffa3824c69"
	review := func(id string, input, cached, output int) string {
		return `{"recorded_at":` + itoa(int(now.UnixMicro())) + `,"payload_type":"runtime.session","payload":{"kind":"approval","event":{"kind":"automated_review_completed",` +
			`"review_id":"` + id + `","status":"approved","model":{"provider_id":"meta","model_id":"muse-spark-1.3","reasoning_effort":"low"},` +
			`"usage":{"input_tokens":` + itoa(input) + `,"cached_input_tokens":` + itoa(cached) + `,"non_cached_input_tokens":` + itoa(input-cached) +
			`,"output_tokens":` + itoa(output) + `,"reasoning_tokens":392,"total_tokens":` + itoa(input+output) + `}}}}`
	}
	write(t, filepath.Join(root, "2026/11/02", session, "session.jsonl"),
		museModel("muse-wonder-1.0"), museCall(now, "usage-1", 31547, 0, 127),
		`{"recorded_at":1,"payload_type":"runtime.session","payload":{"kind":"approval","event":{"kind":"automated_review_started","review_id":"r1"}}}`,
		review("r1", 41403, 1000, 543),
		`{"recorded_at":1,"payload_type":"runtime.session","payload":{"kind":"run","event":{"kind":"model_completed","model":"muse-wonder-1.0","usage":{"input_tokens":31547}}}}`)

	records, skipped := newMuseReader().read(root, now.Add(-time.Hour))
	if skipped != 0 || len(records) != 2 {
		t.Fatalf("records = %+v, skipped %d", records, skipped)
	}
	r := records[1]
	if r.Session != session || r.Model != "muse-spark-1.3" || r.Provider != "meta" || r.Key != "muse:review:r1" ||
		r.Tokens != (protocol.Tokens{Input: 40403, CacheRead: 1000, Output: 543}) {
		t.Fatalf("review = %+v", r)
	}
	if records[0].Model != "muse-wonder-1.0" {
		t.Fatalf("the turn's call = %+v", records[0])
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
