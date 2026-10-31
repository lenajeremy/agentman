package usage

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func write(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendLines(t *testing.T, path string, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func claudeLine(id, at, model string, input, write, write1h, read, output int) string {
	return `{"type":"assistant","timestamp":"` + at + `","sessionId":"s1","requestId":"req_` + id + `","message":{"id":"msg_` + id +
		`","model":"` + model + `","usage":{"input_tokens":` + itoa(input) + `,"cache_creation_input_tokens":` + itoa(write) +
		`,"cache_read_input_tokens":` + itoa(read) + `,"output_tokens":` + itoa(output) +
		`,"cache_creation":{"ephemeral_1h_input_tokens":` + itoa(write1h) + `,"ephemeral_5m_input_tokens":0}}}}`
}

func itoa(n int) string { return strconv.Itoa(n) }

var prices = Catalog{
	"anthropic": {"claude-opus-5-5": {Input: 4, Output: 20, CacheRead: 0.2, CacheWrite: 5}},
	"openai": {"gpt-6.1-sol": {Input: 2, Output: 10, CacheRead: 0.1, CacheWrite: 2.5,
		Above: &Price{Input: 4, Output: 15, CacheRead: 0.2, CacheWrite: 5}, AboveTokens: 272000}},
}

// A Claude reply is counted once, however many lines and files repeat it:
// a resumed session carries its earlier calls into its new file.
func TestClaudeCallsAreReadOnceEach(t *testing.T) {
	home := t.TempDir()
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	reply := claudeLine("a", at, "claude-opus-5-5", 10, 1000, 600, 5000, 200)
	write(t, filepath.Join(home, ".claude/projects/p/one.jsonl"), reply, reply, `{"type":"user","message":{"content":"hi"}}`)
	write(t, filepath.Join(home, ".claude/projects/p/two.jsonl"), reply,
		claudeLine("b", at, "claude-opus-5-5", 5, 0, 0, 100, 50),
		claudeLine("c", at, "<synthetic>", 1, 0, 0, 0, 1),
		`{"type":"assistant","timestamp":"bad","message":{"model":"claude-opus-5-5","usage":{"input_tokens":1}}}`)
	s := NewScanner(home, WithCatalog(prices))
	u := s.Summary(context.Background(), 7, time.Now())
	if u.Tokens != (protocol.Tokens{Input: 15, CacheWrite: 1000, CacheRead: 5100, Output: 250}) {
		t.Errorf("tokens = %+v", u.Tokens)
	}
	if len(u.Agents) != 1 || u.Agents[0].Kind != protocol.KindClaude || u.Agents[0].Sessions != 1 {
		t.Errorf("agents = %+v", u.Agents)
	}
	if u.Skipped != 1 {
		t.Errorf("skipped = %d, want the line with a bad time", u.Skipped)
	}
	// 1h writes at twice the input price, 5m writes at the cache write price.
	want := (15*4 + 400*5 + 600*8 + 5100*0.2 + 250*20) / 1e6
	if math.Abs(u.Cost.Total()-want) > 1e-9 {
		t.Errorf("cost = %v, want %v", u.Cost.Total(), want)
	}
	if math.Abs(u.CacheSavings-5100*(4-0.2)/1e6) > 1e-9 {
		t.Errorf("savings = %v", u.CacheSavings)
	}
}

// Codex keeps running totals: each call is the change since the last, its
// cached input counted apart; totals repeated add nothing, and totals that
// start again stand on the last call.
func TestCodexCallsAreTheChangeInItsTotals(t *testing.T) {
	home := t.TempDir()
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	count := func(in, cached, out, lastIn, lastCached, lastOut int) string {
		return `{"timestamp":"` + at + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":` + itoa(in) +
			`,"cached_input_tokens":` + itoa(cached) + `,"output_tokens":` + itoa(out) + `},"last_token_usage":{"input_tokens":` + itoa(lastIn) +
			`,"cached_input_tokens":` + itoa(lastCached) + `,"output_tokens":` + itoa(lastOut) + `}}}}`
	}
	write(t, filepath.Join(home, ".codex/sessions/2026/10/31/rollout-x.jsonl"),
		`{"timestamp":"`+at+`","type":"turn_context","payload":{"model":"gpt-6.1-sol"}}`,
		count(1000, 800, 10, 1000, 800, 10),
		count(1000, 800, 10, 1000, 800, 10),
		count(3000, 2500, 30, 2000, 1700, 20),
		count(100, 0, 5, 100, 0, 5),
	)
	records, _ := NewScanner(home, WithCatalog(prices)).Records(context.Background(), time.Now().Add(-24*time.Hour))
	var got []protocol.Tokens
	for _, r := range records {
		if r.Model != "gpt-6.1-sol" || r.Agent != protocol.KindCodex {
			t.Errorf("record = %+v", r)
		}
		got = append(got, r.Tokens)
	}
	want := []protocol.Tokens{
		{Input: 200, CacheRead: 800, Output: 10},
		{Input: 300, CacheRead: 1700, Output: 20},
		{Input: 100, Output: 5},
	}
	if len(got) != len(want) {
		t.Fatalf("calls = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A long prompt is charged at the dearer price above its threshold.
func TestPricesRisePastTheirContextLength(t *testing.T) {
	p, _ := prices.Lookup("openai", "gpt-6.1-sol")
	short := Cost(Record{Tokens: protocol.Tokens{Input: 1000, Output: 1000}, Context: 1000}, p)
	long := Cost(Record{Tokens: protocol.Tokens{Input: 1000, Output: 1000}, Context: 300000}, p)
	if short.Total() != (1000*2+1000*10)/1e6 || long.Total() != (1000*4+1000*15)/1e6 {
		t.Errorf("short %v, long %v", short.Total(), long.Total())
	}
	if _, ok := prices.Lookup("anthropic", "claude-opus-5-5-20261001"); !ok {
		t.Error("a dated id wasn't found")
	}
	if _, ok := prices.Lookup("anthropic", "claude-opus-5-5-thinking"); !ok {
		t.Error("a longer id wasn't found by its model")
	}
	if _, ok := prices.Lookup("anthropic", "claude-nope"); ok {
		t.Error("an unknown model was priced")
	}
}

// Days are local days, the last day's hours; nothing before the range.
func TestBucketsAreLocalDaysOrTheLastDaysHours(t *testing.T) {
	now := time.Date(2026, 10, 31, 15, 30, 0, 0, time.Local)
	from, step := Window(7, now)
	if !from.Equal(time.Date(2026, 10, 25, 0, 0, 0, 0, time.Local)) || step != 24*time.Hour {
		t.Errorf("7 days from %v by %v", from, step)
	}
	call := func(at time.Time, model string, agent protocol.Kind, session string) Record {
		return Record{Agent: agent, Session: session, Model: model, Provider: "anthropic", At: at, Tokens: protocol.Tokens{Input: 1000, Output: 100}}
	}
	records := []Record{
		call(time.Date(2026, 10, 24, 23, 0, 0, 0, time.Local), "claude-opus-5-5", protocol.KindClaude, "old"),
		call(time.Date(2026, 10, 25, 9, 0, 0, 0, time.Local), "claude-opus-5-5", protocol.KindClaude, "a"),
		call(time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local), "claude-opus-5-5", protocol.KindClaude, "a"),
		call(time.Date(2026, 10, 31, 10, 0, 0, 0, time.Local), "mystery-model", protocol.KindOpenCode, "b"),
	}
	u := Summarize(records, prices, 7, from, step, now, 0)
	if len(u.Buckets) != 7 || u.Sessions != 2 {
		t.Fatalf("buckets %d, sessions %d", len(u.Buckets), u.Sessions)
	}
	if u.Buckets[0].Tokens[protocol.KindClaude] != 1100 || u.Buckets[6].Tokens[protocol.KindClaude] != 1100 || u.Buckets[6].Tokens[protocol.KindOpenCode] != 1100 {
		t.Errorf("buckets = %+v", u.Buckets)
	}
	if u.Agents[0].Kind != protocol.KindClaude || u.Agents[0].Sessions != 1 {
		t.Errorf("agents = %+v", u.Agents)
	}
	var mystery protocol.ModelUsage
	for _, m := range u.Models {
		if m.Model == "mystery-model" {
			mystery = m
		}
	}
	if mystery.Priced || mystery.Cost != 0 || mystery.Tokens.Total() != 1100 {
		t.Errorf("an unpriced model = %+v", mystery)
	}

	from, step = Window(1, now)
	day := Summarize(records, prices, 1, from, step, now, 0)
	if !day.Hourly || len(day.Buckets) != 24 || day.Tokens.Total() != 2200 {
		t.Errorf("the last day: hourly %v, %d buckets, %d tokens", day.Hourly, len(day.Buckets), day.Tokens.Total())
	}
}

// A second read takes only what was added; a line still being written
// waits until it is whole.
func TestALogIsReadFromWhereItStopped(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude/projects/p/s.jsonl")
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	write(t, path, claudeLine("a", at, "claude-opus-5-5", 1, 0, 0, 0, 1))
	s := NewScanner(home, WithCatalog(prices))
	since := time.Now().Add(-24 * time.Hour)
	if records, _ := s.Records(context.Background(), since); len(records) != 1 {
		t.Fatalf("first read: %d", len(records))
	}
	half := claudeLine("b", at, "claude-opus-5-5", 2, 0, 0, 0, 2)
	appendLines(t, path, half[:40])
	if records, _ := s.Records(context.Background(), since); len(records) != 1 {
		t.Fatalf("a half-written line was read: %d", len(records))
	}
	appendLines(t, path, half[40:]+"\n")
	records, skipped := s.Records(context.Background(), since)
	if len(records) != 2 || skipped != 0 {
		t.Fatalf("after the line was finished: %d records, %d skipped", len(records), skipped)
	}
}

// OpenCode's calls come from its database, its reasoning counted as output.
func TestOpenCodesCallsComeFromItsDatabase(t *testing.T) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	now := time.Now().UnixMilli()
	sql := `create table message (id text primary key, session_id text not null, time_created integer not null, time_updated integer not null, data text not null);` +
		`insert into message values ('m1','ses_1',` + itoa(int(now)) + `,0,'{"role":"assistant","modelID":"gpt-6.1-sol","providerID":"openai","tokens":{"input":296,"output":99,"reasoning":92,"cache":{"write":0,"read":50560}}}');` +
		`insert into message values ('m2','ses_1',` + itoa(int(now)) + `,0,'{"role":"user"}');` +
		`insert into message values ('m3','ses_1',1,0,'{"role":"assistant","modelID":"x","providerID":"openai","tokens":{"input":5}}');`
	if out, err := exec.Command(bin, db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, out)
	}
	records, skipped := readOpenCode(context.Background(), db, time.Now().Add(-time.Hour))
	if skipped != 0 || len(records) != 1 {
		t.Fatalf("records = %+v, skipped %d", records, skipped)
	}
	r := records[0]
	if r.Agent != protocol.KindOpenCode || r.Session != "ses_1" || r.Model != "gpt-6.1-sol" || r.Provider != "openai" ||
		r.Tokens != (protocol.Tokens{Input: 296, CacheRead: 50560, Output: 191}) {
		t.Errorf("record = %+v", r)
	}
}
