package usage

import (
	"bytes"
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

// calls is what a log's lines say, call by call, as its reader parses them.
func calls(t *testing.T, path string, parse parser) []Record {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state fileState
	var out []Record
	for _, line := range bytes.Split(data, []byte("\n")) {
		_ = parse(bytes.TrimSpace(line), path, &state, func(r Record) { out = append(out, r) })
	}
	return out
}

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
	path := filepath.Join(home, ".codex/sessions/2026/10/31/rollout-x.jsonl")
	var got []protocol.Tokens
	for _, r := range calls(t, path, parseCodex) {
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
	if u := NewScanner(home, WithCatalog(prices)).Summary(context.Background(), 7, time.Now()); u.Tokens != (protocol.Tokens{Input: 600, CacheRead: 2500, Output: 35}) {
		t.Errorf("summed = %+v", u.Tokens)
	}
}

// A session carried on from another starts with that one's totals, its
// last call nothing: only what it adds is its own.
func TestCodexCarriedTotalsAreNotCountedAgain(t *testing.T) {
	home := t.TempDir()
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	count := func(in, cached, out, lastIn, lastCached, lastOut int) string {
		return `{"timestamp":"` + at + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":` + itoa(in) +
			`,"cached_input_tokens":` + itoa(cached) + `,"output_tokens":` + itoa(out) + `},"last_token_usage":{"input_tokens":` + itoa(lastIn) +
			`,"cached_input_tokens":` + itoa(lastCached) + `,"output_tokens":` + itoa(lastOut) + `}}}}`
	}
	write(t, filepath.Join(home, ".codex/sessions/2026/10/31/rollout-carried.jsonl"),
		`{"timestamp":"`+at+`","type":"session_meta","payload":{"id":"s2"}}`,
		`{"timestamp":"`+at+`","type":"compacted","payload":{"message":"summary"}}`,
		count(900000, 800000, 5000, 0, 0, 0),
		`{"timestamp":"`+at+`","type":"turn_context","payload":{"model":"gpt-6.1-sol"}}`,
		count(903000, 802500, 5030, 3000, 2500, 30),
	)
	records := calls(t, filepath.Join(home, ".codex/sessions/2026/10/31/rollout-carried.jsonl"), parseCodex)
	if len(records) != 1 || records[0].Tokens != (protocol.Tokens{Input: 500, CacheRead: 2500, Output: 30}) || records[0].Model != "gpt-6.1-sol" {
		t.Fatalf("records = %+v", records)
	}
	if u := NewScanner(home, WithCatalog(prices)).Summary(context.Background(), 7, time.Now()); u.Tokens != records[0].Tokens {
		t.Fatalf("summed = %+v", u.Tokens)
	}
}

// Codex's input counts what it wrote to the cache: fresh input is the rest.
func TestCodexCacheWritesAreNotFreshInput(t *testing.T) {
	u := codexUsage{Input: 1000, Cached: 600, CacheWrite: 300, Output: 10}
	if got := u.tokens(); got != (protocol.Tokens{Input: 100, CacheRead: 600, CacheWrite: 300, Output: 10}) {
		t.Fatalf("tokens = %+v", got)
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
	from, unit := Window(7, now)
	if !from.Equal(time.Date(2026, 10, 25, 0, 0, 0, 0, time.Local)) || unit != Day {
		t.Errorf("7 days from %v by %v", from, unit)
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
	u := Summarize(records, prices, 7, from, unit, now, 0)
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

	from, unit = Window(1, now)
	day := Summarize(records, prices, 1, from, unit, now, 0)
	if !day.Hourly || day.Unit != "hour" || len(day.Buckets) != 24 || day.Tokens.Total() != 2200 {
		t.Errorf("the last day: hourly %v, %d buckets, %d tokens", day.Hourly, len(day.Buckets), day.Tokens.Total())
	}
}

// Six months go by day, a year by week from a Monday, and all of it by
// week, or by month once it spans more than two years.
func TestLongerRangesGoByWeekOrMonth(t *testing.T) {
	now := time.Date(2026, 10, 31, 15, 30, 0, 0, time.Local) // a Saturday
	if from, unit := Window(180, now); unit != Day || !from.Equal(time.Date(2026, 5, 5, 0, 0, 0, 0, time.Local)) {
		t.Errorf("180 days from %v by %v", from, unit)
	}
	from, unit := Window(365, now)
	if unit != Week || from.Weekday() != time.Monday || !from.Equal(time.Date(2025, 10, 27, 0, 0, 0, 0, time.Local)) {
		t.Errorf("a year from %v (%v) by %v", from, from.Weekday(), unit)
	}
	call := func(at time.Time) Record {
		return Record{Agent: protocol.KindClaude, Session: "s", Model: "claude-opus-5-5", Provider: "anthropic", At: at, Tokens: protocol.Tokens{Input: 10}}
	}
	year := Summarize([]Record{call(from), call(time.Date(2026, 10, 26, 8, 0, 0, 0, time.Local)), call(now)}, prices, 365, from, unit, now, 0)
	last := year.Buckets[len(year.Buckets)-1]
	if year.Unit != "week" || len(year.Buckets) != 53 || year.Buckets[0].Tokens[protocol.KindClaude] != 10 || last.Tokens[protocol.KindClaude] != 20 {
		t.Errorf("a year: %s, %d buckets, first %v, last %v", year.Unit, len(year.Buckets), year.Buckets[0].Tokens, last.Tokens)
	}

	if from, unit := AllTime(time.Date(2026, 3, 4, 12, 0, 0, 0, time.Local), now); unit != Week || !from.Equal(time.Date(2026, 3, 2, 0, 0, 0, 0, time.Local)) {
		t.Errorf("all of it from %v by %v", from, unit)
	}
	if from, unit := AllTime(time.Date(2023, 6, 15, 12, 0, 0, 0, time.Local), now); unit != Month || !from.Equal(time.Date(2023, 6, 1, 0, 0, 0, 0, time.Local)) {
		t.Errorf("years of it from %v by %v", from, unit)
	}
}

// All of it begins at the first record.
func TestAllOfItBeginsAtTheFirstRecord(t *testing.T) {
	home := t.TempDir()
	old := time.Now().AddDate(0, -8, 0)
	path := filepath.Join(home, ".claude/projects/p/old.jsonl")
	write(t, path, claudeLine("a", old.UTC().Format(time.RFC3339Nano), "claude-opus-5-5", 10, 0, 0, 0, 1))
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	s := NewScanner(home, WithCatalog(prices))
	if u := s.Summary(context.Background(), 30, time.Now()); u.Tokens.Total() != 0 {
		t.Fatalf("30 days holds nothing from 8 months ago: %d", u.Tokens.Total())
	}
	all := s.Summary(context.Background(), 0, time.Now())
	if all.Days != 0 || all.Unit != "week" || all.Tokens.Total() != 11 || len(all.Buckets) < 34 {
		t.Fatalf("all of it: unit %s, %d tokens, %d buckets", all.Unit, all.Tokens.Total(), len(all.Buckets))
	}
	if first := time.UnixMilli(all.Buckets[0].Start); first.After(old) || old.Sub(first) > 7*24*time.Hour {
		t.Fatalf("starts %v, the record %v", first, old)
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
	if u := s.Summary(context.Background(), 1, time.Now()); u.Tokens.Total() != 2 {
		t.Fatalf("first read: %d tokens", u.Tokens.Total())
	}
	half := claudeLine("b", at, "claude-opus-5-5", 2, 0, 0, 0, 2)
	appendLines(t, path, half[:40])
	if u := s.Summary(context.Background(), 1, time.Now()); u.Tokens.Total() != 2 || u.Skipped != 0 {
		t.Fatalf("a half-written line was read: %d tokens, %d skipped", u.Tokens.Total(), u.Skipped)
	}
	appendLines(t, path, half[40:]+"\n")
	if u := s.Summary(context.Background(), 1, time.Now()); u.Tokens.Total() != 6 || u.Skipped != 0 {
		t.Fatalf("after the line was finished: %d tokens, %d skipped", u.Tokens.Total(), u.Skipped)
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

// A call summed in its log, then found in another (a session resumed in a
// new file carries its earlier calls along), is still counted once; and
// still once when the first log goes.
func TestACallCarriedToAnotherLogIsCountedOnce(t *testing.T) {
	home := t.TempDir()
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	carried := claudeLine("a", at, "claude-opus-5-5", 10, 0, 0, 0, 1)
	first := filepath.Join(home, ".claude/projects/p/b.jsonl")
	write(t, first, carried, carried, claudeLine("b", at, "claude-opus-5-5", 100, 0, 0, 0, 10))
	s := NewScanner(home, WithCatalog(prices))
	ctx := context.Background()
	if u := s.Summary(ctx, 7, time.Now()); u.Tokens.Total() != 121 {
		t.Fatalf("one log: %d tokens", u.Tokens.Total())
	}
	// One log read before the first, one after: the call is counted once.
	for _, name := range []string{"a.jsonl", "c.jsonl"} {
		write(t, filepath.Join(home, ".claude/projects/p", name), carried, claudeLine(name, at, "claude-opus-5-5", 1000, 0, 0, 0, 100))
	}
	if u := s.Summary(ctx, 7, time.Now()); u.Tokens.Total() != 121+2*1100 {
		t.Fatalf("carried into two more logs: %d tokens", u.Tokens.Total())
	}
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	if u := s.Summary(ctx, 7, time.Now()); u.Tokens.Total() != 11+2*1100 {
		t.Fatalf("the first log gone: %d tokens", u.Tokens.Total())
	}
	if u := NewScanner(home, WithCatalog(prices)).Summary(ctx, 7, time.Now()); u.Tokens.Total() != 11+2*1100 {
		t.Fatalf("read afresh: %d tokens", u.Tokens.Total())
	}
}

// Calls are summed by the hour, or by the half hour where the clock is half
// an hour off UTC: days start at local midnight and the last day goes by
// UTC hours, and each sum falls in one bucket of either.
func TestSumsFallInOneBucketOfAnyClock(t *testing.T) {
	india := time.FixedZone("IST", 5*3600+1800)
	home := t.TempDir()
	day := time.Date(2026, 10, 30, 0, 0, 0, 0, india)
	stamp := func(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
	write(t, filepath.Join(home, ".claude/projects/p/s.jsonl"),
		claudeLine("a", stamp(day.Add(-10*time.Minute)), "claude-opus-5-5", 1, 0, 0, 0, 0),
		claudeLine("b", stamp(day.Add(10*time.Minute)), "claude-opus-5-5", 2, 0, 0, 0, 0),
		claudeLine("c", stamp(day.Add(15*time.Hour+15*time.Minute)), "claude-opus-5-5", 4, 0, 0, 0, 0),
		claudeLine("d", stamp(day.Add(15*time.Hour+45*time.Minute)), "claude-opus-5-5", 8, 0, 0, 0, 0),
	)
	now := day.Add(20 * time.Hour)
	s := NewScanner(home, WithCatalog(prices))
	week := s.Summary(context.Background(), 7, now)
	if n := len(week.Buckets); week.Buckets[n-1].Tokens[protocol.KindClaude] != 14 || week.Buckets[n-2].Tokens[protocol.KindClaude] != 1 {
		t.Errorf("by local day: %+v", week.Buckets)
	}
	// 15:15 and 15:45 in India are 09:45 and 10:15 UTC: two hours.
	hours := map[int64]int64{}
	for _, b := range s.Summary(context.Background(), 1, now).Buckets {
		if n := b.Tokens[protocol.KindClaude]; n > 0 {
			hours[b.Start] = n
		}
	}
	nine := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC).UnixMilli()
	if hours[nine] != 4 || hours[nine+3600_000] != 8 {
		t.Errorf("by the hour: %v", hours)
	}
}

// A long prompt read from a log is charged the dearer price, past the length
// the catalog says: when that changes, the logs are summed again.
func TestLongPromptsAreToldApartAtTheCatalogsLength(t *testing.T) {
	home := t.TempDir()
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	count := func(in, out int) string {
		return `{"timestamp":"` + at + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":` + itoa(in) +
			`,"output_tokens":` + itoa(out) + `},"last_token_usage":{"input_tokens":` + itoa(in) + `,"output_tokens":` + itoa(out) + `}}}}`
	}
	write(t, filepath.Join(home, ".codex/sessions/2026/10/31/rollout-long.jsonl"),
		`{"timestamp":"`+at+`","type":"turn_context","payload":{"model":"gpt-6.1-sol"}}`,
		count(300000, 1000))
	s := NewScanner(home, WithCatalog(prices))
	if got, want := s.Summary(context.Background(), 7, time.Now()).Cost.Total(), (300000*4+1000*15)/1e6; math.Abs(got-want) > 1e-9 {
		t.Errorf("past 272K: $%v, want $%v", got, want)
	}
	price := prices["openai"]["gpt-6.1-sol"]
	price.AboveTokens = 400000
	s.prices = WithCatalog(Catalog{"openai": {"gpt-6.1-sol": price}})
	if got, want := s.Summary(context.Background(), 7, time.Now()).Cost.Total(), (300000*2+1000*10)/1e6; math.Abs(got-want) > 1e-9 {
		t.Errorf("under 400K: $%v, want $%v", got, want)
	}
}

// A reply's output grows from line to line as it streams. It counts once,
// at its first line's time, with the most its lines count: read at once or
// a read between its lines, across a slot's edge or a range's start, and
// with a copy in another log that holds only its first line.
func TestAReplyCountsItsFinalOutputAtItsFirstLine(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 31, 15, 30, 0, 0, time.Local)
	start, _ := Window(7, now)
	line := func(id string, at time.Time, output int) string {
		return claudeLine(id, at.UTC().Format(time.RFC3339Nano), "claude-opus-5-5", 2, 0, 0, 5000, output)
	}
	// a runs past the week's start; b, in the week, past an hour's end.
	a := []string{line("a", start.Add(-time.Second), 8), line("a", start.Add(time.Second), 60), line("a", start.Add(2*time.Second), 100)}
	day := start.AddDate(0, 0, 3)
	b := []string{line("b", day.Add(59*time.Minute), 8), line("b", day.Add(61*time.Minute), 300)}
	week := protocol.Tokens{Input: 2, CacheRead: 5000, Output: 300}
	month := protocol.Tokens{Input: 4, CacheRead: 10000, Output: 400}
	for _, apart := range []bool{false, true} {
		home := t.TempDir()
		path := filepath.Join(home, ".claude/projects/p/s.jsonl")
		s := NewScanner(home, WithCatalog(prices))
		if apart {
			write(t, path, a[0], b[0])
			s.Summary(ctx, 30, now)
			appendLines(t, path, a[1]+"\n")
			s.Summary(ctx, 7, now)
			appendLines(t, path, a[2]+"\n"+b[1]+"\n")
		} else {
			write(t, path, append(a, b...)...)
		}
		for _, copied := range []bool{false, true} {
			if copied {
				write(t, filepath.Join(home, ".claude/projects/p/copy.jsonl"), a[0], b[0])
			}
			u := s.Summary(ctx, 7, now)
			if u.Tokens != week || u.Buckets[3].Tokens[protocol.KindClaude] != week.Total() {
				t.Errorf("apart %v, copied %v, the week: %+v, its fourth day %d", apart, copied, u.Tokens, u.Buckets[3].Tokens[protocol.KindClaude])
			}
			if u := s.Summary(ctx, 30, now); u.Tokens != month || math.Abs(u.Cost.Total()-(4*4+10000*0.2+400*20)/1e6) > 1e-12 {
				t.Errorf("apart %v, copied %v, the month: %+v, $%v", apart, copied, u.Tokens, u.Cost.Total())
			}
		}
	}
}

// A Claude turn in fast mode is charged twice its model's price for every
// kind of token (six times on Opus 4.6, where fast mode began). An advisor
// consulted during a turn is counted apart from the turn, under its own
// model and at its prices, once however many lines repeat it.
func TestClaudesFastModeAndAdvisorsAreCountedAtTheirPrices(t *testing.T) {
	home := t.TempDir()
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	turn := func(id, model, usage string) string {
		return `{"type":"assistant","timestamp":"` + at + `","sessionId":"s1","requestId":"req_` + id + `","advisorModel":"claude-opus-5-5",` +
			`"message":{"id":"msg_` + id + `","model":"` + model + `","usage":{` + usage + `}}}`
	}
	fast := turn("f", "claude-opus-5-5", `"input_tokens":10,"cache_creation_input_tokens":1000,"cache_read_input_tokens":5000,"output_tokens":200,`+
		`"cache_creation":{"ephemeral_1h_input_tokens":600,"ephemeral_5m_input_tokens":400},"service_tier":"standard","speed":"fast",`+
		`"iterations":[{"input_tokens":10,"output_tokens":200,"cache_read_input_tokens":5000,"cache_creation_input_tokens":1000,"type":"message"}]`)
	fastOld := turn("o", "claude-opus-4-6", `"input_tokens":100,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":100,"speed":"fast"`)
	// The usage the advisor's documentation shows: the turn's own counts are
	// its two executor iterations; the advisor's are apart.
	executor := `"input_tokens":1760,"cache_read_input_tokens":412,"cache_creation_input_tokens":0,"output_tokens":531,"speed":"standard"`
	advised := turn("a", "claude-sonnet-5-5", executor+`,"iterations":[`+
		`{"type":"message","input_tokens":412,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":89},`+
		`{"type":"advisor_message","model":"claude-opus-5-5","input_tokens":823,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":1612},`+
		`{"type":"message","input_tokens":1348,"cache_read_input_tokens":412,"cache_creation_input_tokens":0,"output_tokens":442}]`)
	// A reply's first line may come before its advisor was consulted.
	write(t, filepath.Join(home, ".claude/projects/p/s1.jsonl"), fast, fast, fastOld, turn("a", "claude-sonnet-5-5", executor), advised, advised)
	catalog := Catalog{"anthropic": {
		"claude-opus-5-5":   prices["anthropic"]["claude-opus-5-5"],
		"claude-opus-4-6":   {Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25},
		"claude-sonnet-5-5": {Input: 2, Output: 10, CacheRead: 0.1, CacheWrite: 2.5},
	}}
	u := NewScanner(home, WithCatalog(catalog)).Summary(context.Background(), 7, time.Now())

	fastCost := 2 * (10*4 + 400*5 + 600*8 + 5000*0.2 + 200*20) / 1e6
	fastOldCost := 6 * (100*5 + 100*25) / 1e6
	turnCost := (1760*2 + 412*0.1 + 531*10) / 1e6
	adviceCost := (823*4 + 1612*20) / 1e6
	if got, want := u.Cost.Total(), fastCost+fastOldCost+turnCost+adviceCost; math.Abs(got-want) > 1e-9 {
		t.Errorf("cost = %v, want %v", got, want)
	}
	if want := (protocol.Tokens{Input: 10 + 100 + 1760 + 823, CacheRead: 5000 + 412, CacheWrite: 1000, Output: 200 + 100 + 531 + 1612}); u.Tokens != want {
		t.Errorf("tokens = %+v, want %+v", u.Tokens, want)
	}
	models := map[string]protocol.ModelUsage{}
	for _, m := range u.Models {
		models[m.Model] = m
	}
	opus := models["claude-opus-5-5"]
	if opus.Fast == nil || opus.Advisor == nil || opus.Fast.Tokens.Total() != 6210 || opus.Advisor.Tokens.Total() != 2435 ||
		opus.Tokens.Total() != 6210+2435 || math.Abs(opus.Fast.Cost-fastCost) > 1e-9 || math.Abs(opus.Advisor.Cost-adviceCost) > 1e-9 ||
		math.Abs(opus.Cost-fastCost-adviceCost) > 1e-9 {
		t.Errorf("Opus 5.5, fast and advising: %+v, fast %+v, advisor %+v", opus, opus.Fast, opus.Advisor)
	}
	if old := models["claude-opus-4-6"]; old.Fast == nil || old.Fast.Tokens.Total() != 200 || math.Abs(old.Cost-fastOldCost) > 1e-9 {
		t.Errorf("Opus 4.6 fast: %+v", old)
	}
	if sonnet := models["claude-sonnet-5-5"]; sonnet.Fast != nil || sonnet.Advisor != nil || sonnet.Tokens.Total() != 2703 || math.Abs(sonnet.Cost-turnCost) > 1e-9 {
		t.Errorf("the advised turn: %+v", sonnet)
	}
	if len(u.Models) != 3 || u.Sessions != 1 {
		t.Errorf("%d models, %d sessions", len(u.Models), u.Sessions)
	}
}

// A reply's first lines may not say its speed: one that turns out to have
// run fast is charged as fast, though its first line was read and summed
// before the line that says so was written.
func TestAReplyIsFastWhenALaterLineSaysSo(t *testing.T) {
	home := t.TempDir()
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	reply := func(speed string, output int) string {
		return `{"type":"assistant","timestamp":"` + at + `","sessionId":"s1","requestId":"req_f","message":{"id":"msg_f","model":"claude-opus-5-5",` +
			`"usage":{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":` + itoa(output) + speed + `}}}`
	}
	path := filepath.Join(home, ".claude/projects/p/s.jsonl")
	write(t, path, reply("", 8))
	s := NewScanner(home, WithCatalog(prices))
	if u := s.Summary(context.Background(), 7, time.Now()); math.Abs(u.Cost.Total()-(10*4+8*20)/1e6) > 1e-12 {
		t.Fatalf("its first line: $%v", u.Cost.Total())
	}
	appendLines(t, path, reply(`,"speed":"fast"`, 500)+"\n")
	u := s.Summary(context.Background(), 7, time.Now())
	if want := 2 * (10*4 + 500*20) / 1e6; u.Tokens != (protocol.Tokens{Input: 10, Output: 500}) || math.Abs(u.Cost.Total()-want) > 1e-12 ||
		len(u.Models) != 1 || u.Models[0].Fast == nil || u.Models[0].Fast.Tokens.Total() != 510 {
		t.Errorf("its last line ran fast: %+v, $%v, %+v", u.Tokens, u.Cost.Total(), u.Models)
	}
}
