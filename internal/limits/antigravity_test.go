package limits

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// sampleAgyUsage is `agy -p "/usage" --output-format json` as agy 1.5.1
// answers it, its descriptions cut short.
const sampleAgyUsage = `{"conversation_id":"","status":"SUCCESS","response":"Gemini Models\tWeekly Limit Remaining\t99%\t2026-11-09T09:51:24Z\n","duration_seconds":0,"num_turns":0,` +
	`"usage":{"input_tokens":0,"output_tokens":0,"thinking_tokens":0,"cache_read_tokens":0,"total_tokens":0},` +
	`"command":{"name":"usage","data":{"description":"Within each group, models share a weekly limit and a 5-hour limit.","groups":[` +
	`{"name":"Gemini Models","description":"Models within this group: Gemini Flash, Gemini Pro","buckets":[` +
	`{"id":"gemini-weekly","name":"Weekly Limit Remaining","description":"You have used some of your weekly limit.","window":"weekly","remaining_fraction":0.985055685043335,"reset_time":"2026-11-09T09:51:24Z"},` +
	`{"id":"gemini-5h","name":"Five Hour Limit Remaining","window":"5h","remaining_fraction":1,"reset_time":"2026-11-05T17:05:42Z"}]},` +
	`{"name":"Claude and GPT models","description":"Models within this group: Claude Opus, Claude Sonnet, GPT-OSS","buckets":[` +
	`{"id":"3p-weekly","name":"Weekly Limit Remaining","window":"weekly","remaining_fraction":0.4963464140892029,"reset_time":"2026-11-11T10:36:59Z"},` +
	`{"id":"3p-5h","name":"Five Hour Limit Remaining","window":"5h","remaining_fraction":1,"reset_time":"2026-11-05T17:05:42Z"}]},` +
	`{"name":"Gemini 4 Argon","description":"For paid Pro plans, Argon quota is separate.","buckets":[` +
	`{"id":"gemini-argon-wtu-weekly","name":"Gemini 4 Argon Weekly Limit","window":"weekly","remaining_fraction":0.8874846696853638,"reset_time":"2026-11-09T09:50:22Z"}]}]}}}`

func decodeAgy(t *testing.T, text string) agyUsage {
	t.Helper()
	usage, err := parseAgyUsage([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return usage
}

// Each group's windows, named by window and group, with the share used. A
// window not used yet says it resets a whole window from when it was asked:
// its clock hasn't started, so it shows no reset.
func TestAntigravityLimitsFromItsUsage(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 5, 41, 0, time.UTC)
	agent := antigravityLimits(decodeAgy(t, sampleAgyUsage), now)
	if agent.Kind != protocol.KindAntigravity || agent.CheckedAt != now.UnixMilli() || agent.Problem != "" || agent.Plan != "" {
		t.Fatalf("agent = %+v", agent)
	}
	var ids, labels []string
	for _, window := range agent.Windows {
		ids = append(ids, window.ID)
		labels = append(labels, window.Label)
	}
	if want := []string{"gemini-weekly", "gemini-5h", "3p-weekly", "3p-5h", "gemini-argon-wtu-weekly"}; !slices.Equal(ids, want) {
		t.Fatalf("ids = %v", ids)
	}
	if want := []string{"Weekly · Gemini", "Session · Gemini", "Weekly · Claude & GPT", "Session · Claude & GPT", "Weekly · Gemini 4 Argon"}; !slices.Equal(labels, want) {
		t.Errorf("labels = %v", labels)
	}
	week, session, third := agent.Windows[0], agent.Windows[1], agent.Windows[2]
	if week.Kind != "weekly" || week.Minutes != 10080 || week.Used < 1.49 || week.Used > 1.5 ||
		week.ResetsAt != time.Date(2026, 11, 9, 9, 51, 24, 0, time.UTC).UnixMilli() {
		t.Errorf("week = %+v", week)
	}
	if session.Kind != "session" || session.Minutes != 300 || session.Used != 0 || session.ResetsAt != 0 {
		t.Errorf("session = %+v", session)
	}
	if third.Used < 50.36 || third.Used > 50.37 {
		t.Errorf("Claude and GPT's week = %+v", third)
	}
	// Used, the five hours keep their reset.
	used := strings.Replace(sampleAgyUsage, `"window":"5h","remaining_fraction":1,"reset_time":"2026-11-05T17:05:42Z"`,
		`"window":"5h","remaining_fraction":0.8,"reset_time":"2026-11-05T14:00:00Z"`, 1)
	if session := antigravityLimits(decodeAgy(t, used), now).Windows[1]; session.ResetsAt != time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC).UnixMilli() ||
		session.Used < 19.99 || session.Used > 20.01 {
		t.Errorf("used session = %+v", session)
	}
}

// A window not known keeps its bucket's name; shares stay between 0 and 100;
// a bucket without a share is left out; no windows says so.
func TestAntigravityOddWindows(t *testing.T) {
	now := time.Now()
	agent := antigravityLimits(decodeAgy(t, `{"status":"SUCCESS","command":{"name":"usage","data":{"groups":[
	 {"name":"Gemini Models","buckets":[{"name":"Fortnight Limit","window":"14d","remaining_fraction":-0.5},
	  {"id":"no-share","window":"weekly"},{"id":"day","window":"24h","remaining_fraction":0.25}]}]}}}`), now)
	if len(agent.Windows) != 2 {
		t.Fatalf("windows = %+v", agent.Windows)
	}
	odd, day := agent.Windows[0], agent.Windows[1]
	if odd.ID != "gemini_models_14d" || odd.Kind != "other" || odd.Label != "Fortnight Limit · Gemini" || odd.Used != 100 || odd.Minutes != 0 {
		t.Errorf("odd = %+v", odd)
	}
	if day.Kind != "other" || day.Label != "Daily · Gemini" || day.Used != 75 || day.Minutes != 1440 || day.ResetsAt != 0 {
		t.Errorf("day = %+v", day)
	}
	none := antigravityLimits(decodeAgy(t, `{"status":"SUCCESS","command":{"name":"usage","data":{"groups":[]}}}`), now)
	if len(none.Windows) != 0 || !strings.Contains(none.Problem, "no limits") {
		t.Errorf("none = %+v", none)
	}
}

func TestAgyVersionsThatAnswerInPrint(t *testing.T) {
	for version, want := range map[string]bool{
		"1.5.1": true, "1.1.11\n": true, "1.1.10": false, "1.2.0": true, "1.10.0": true, "0.9.30": false,
		"agy version 2.0.0": true, "": false, "unknown": false,
	} {
		if got := agyAnswersInPrint(version); got != want {
			t.Errorf("%q answers in print: %v, want %v", version, got, want)
		}
	}
}

// fakeAgy writes a stand-in for agy that says its version, answers /usage,
// and notes what it was started with and where.
func fakeAgy(t *testing.T, version, answer string) (binary, noted string) {
	t.Helper()
	dir := t.TempDir()
	binary = filepath.Join(dir, "agy")
	noted = filepath.Join(dir, "noted")
	answerFile := filepath.Join(dir, "answer.json")
	if err := os.WriteFile(answerFile, []byte(answer), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$@\" >> '" + noted + "'\npwd >> '" + noted + "'\n" +
		"if [ \"$1\" = --version ]; then echo '" + version + "'; exit 0; fi\n" +
		"cat '" + answerFile + "'\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return binary, noted
}

// agy is asked its version, then /usage in print mode as JSON, in an empty
// folder of its own that is gone afterwards.
func TestAskAntigravity(t *testing.T) {
	binary, noted := fakeAgy(t, "1.5.1", "\n"+sampleAgyUsage+"\n")
	usage, err := askAntigravity(context.Background(), binary, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if len(usage.Command.Data.Groups) != 3 {
		t.Fatalf("usage = %+v", usage)
	}
	data, err := os.ReadFile(noted)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4 || lines[0] != "--version" || lines[2] != "-p /usage --output-format json --print-timeout 15s" {
		t.Fatalf("started as %q", lines)
	}
	folder := lines[3]
	if !strings.Contains(filepath.Base(folder), "agentman-agy-limits-") {
		t.Errorf("asked in %s", folder)
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Errorf("left %s behind: %v", folder, err)
	}
}

// An agy too old to answer in print mode is never asked /usage, which it
// would send to a model; one that started a turn or failed says so.
func TestAskAntigravityRefusals(t *testing.T) {
	binary, noted := fakeAgy(t, "1.1.10", sampleAgyUsage)
	if _, err := askAntigravity(context.Background(), binary, os.Environ()); !errors.Is(err, errAgyTooOld) {
		t.Errorf("too old: %v", err)
	}
	if data, _ := os.ReadFile(noted); strings.Contains(string(data), "/usage") {
		t.Errorf("asked an old agy /usage: %q", data)
	}
	turned := strings.Replace(sampleAgyUsage, `"conversation_id":""`, `"conversation_id":"c1"`, 1)
	binary, _ = fakeAgy(t, "1.5.1", strings.Replace(turned, `"num_turns":0`, `"num_turns":1`, 1))
	if _, err := askAntigravity(context.Background(), binary, os.Environ()); !errors.Is(err, errAgyTurn) {
		t.Errorf("a turn: %v", err)
	}
	binary, _ = fakeAgy(t, "1.5.1", `{"conversation_id":"","status":"ERROR","response":"Not signed in.","num_turns":0}`)
	if _, err := askAntigravity(context.Background(), binary, os.Environ()); !errors.Is(err, errAgyFailed) {
		t.Errorf("failed: %v", err)
	}
	binary, _ = fakeAgy(t, "1.5.1", "not json")
	if _, err := askAntigravity(context.Background(), binary, os.Environ()); err == nil {
		t.Error("read an answer from nothing")
	}
}

// An agy that doesn't answer is given up on with all it started.
func TestAskAntigravityGivesUpOnSilence(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "agy")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nsleep 30 &\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := askAntigravity(ctx, binary, os.Environ()); err == nil {
		t.Fatal("answered from silence")
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Errorf("waited %v", waited)
	}
}

// stubAgy stands in for asking agy.
type stubAgy struct {
	asks   int
	answer agyUsage
	err    error
}

func (s *stubAgy) ask(context.Context) (agyUsage, error) {
	s.asks++
	return s.answer, s.err
}

// agy on the PATH is asked with the others and shown after them; when it
// can't be asked, its last numbers are shown with why; an old agy says to
// update it.
func TestReaderAsksAntigravity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	stub := &stubAgy{answer: decodeAgy(t, sampleAgyUsage)}
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	r := NewReader(t.TempDir())
	r.antigravity = stub.ask
	r.now = func() time.Time { return now }

	good := r.Read(context.Background(), false)
	if len(good.Agents) != 1 || good.Agents[0].Kind != protocol.KindAntigravity || len(good.Agents[0].Windows) != 5 || stub.asks != 1 {
		t.Fatalf("good = %+v after %d asks", good, stub.asks)
	}
	stub.err = errors.New("offline")
	now = now.Add(keep)
	failed := r.Read(context.Background(), false).Agents[0]
	if len(failed.Windows) != 5 || failed.CheckedAt != good.Agents[0].CheckedAt || !strings.Contains(failed.Problem, "didn't say") {
		t.Errorf("failed = %+v", failed)
	}
	stub.err = errAgyFailed
	now = now.Add(keep)
	if refused := r.Read(context.Background(), false).Agents[0]; !strings.Contains(refused.Problem, "couldn't read") || len(refused.Windows) != 5 {
		t.Errorf("refused = %+v", refused)
	}
	stub.err = errAgyTooOld
	now = now.Add(keep)
	if old := r.Read(context.Background(), false).Agents[0]; !strings.Contains(old.Problem, "agy update") || len(old.Windows) != 0 {
		t.Errorf("old = %+v", old)
	}
}
