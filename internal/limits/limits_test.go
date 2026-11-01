package limits

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// sampleUsage is get_usage's answer as Claude Code 2.1.316 gives it, behaviors
// left out as asked.
const sampleUsage = `{"session":{"total_cost_usd":0},"subscription_type":"max","rate_limits_available":true,
 "rate_limits":{"five_hour":{"utilization":37,"resets_at":"2026-11-01T09:00:00.000Z"},
  "seven_day":{"utilization":62.5,"resets_at":"2026-11-05T17:00:00+00:00"},
  "seven_day_oauth_apps":null,"seven_day_opus":{"utilization":12,"resets_at":null},"seven_day_sonnet":null,
  "model_scoped":[{"display_name":"Opus","utilization":12,"resets_at":"2026-11-05T17:00:00Z"},
   {"display_name":"Fable 5","utilization":140,"resets_at":null},{"display_name":"Empty","utilization":null,"resets_at":null}],
  "extra_usage":{"is_enabled":true,"monthly_limit":5000,"used_credits":1250,"utilization":null,"currency":"USD"}},
 "behaviors":null}`

func decodeUsage(t *testing.T, text string) claudeUsage {
	t.Helper()
	var usage claudeUsage
	if err := json.Unmarshal([]byte(text), &usage); err != nil {
		t.Fatal(err)
	}
	return usage
}

// The five hours first, then the week, each model's week once however it is
// named, and paid usage past the plan; shares kept between 0 and 100.
func TestClaudeLimitsAreReadFromItsUsage(t *testing.T) {
	now := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	agent := claudeLimits(decodeUsage(t, sampleUsage), now)
	if agent.Kind != protocol.KindClaude || agent.Plan != "Max" || agent.CheckedAt != now.UnixMilli() || agent.Problem != "" {
		t.Fatalf("agent = %+v", agent)
	}
	var ids, labels []string
	for _, window := range agent.Windows {
		ids = append(ids, window.ID)
		labels = append(labels, window.Label)
	}
	if want := []string{"five_hour", "seven_day", "seven_day_opus", "seven_day_fable_5", "extra_usage"}; !slices.Equal(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	if want := []string{"Session", "Weekly", "Weekly · Opus", "Weekly · Fable 5", "Extra usage"}; !slices.Equal(labels, want) {
		t.Errorf("labels = %v", labels)
	}
	session, week := agent.Windows[0], agent.Windows[1]
	if session.Kind != "session" || session.Used != 37 || session.Minutes != 300 ||
		session.ResetsAt != time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC).UnixMilli() {
		t.Errorf("session = %+v", session)
	}
	if week.Kind != "weekly" || week.Used != 62.5 || week.Minutes != 10080 ||
		week.ResetsAt != time.Date(2026, 11, 5, 17, 0, 0, 0, time.UTC).UnixMilli() {
		t.Errorf("week = %+v", week)
	}
	if fable := agent.Windows[3]; fable.Used != 100 || fable.ResetsAt != 0 {
		t.Errorf("a share over 100 is 100, an unknown reset 0: %+v", fable)
	}
	if extra := agent.Windows[4]; extra.Kind != "monthly" || extra.Used != 25 || extra.Minutes != 0 {
		t.Errorf("extra usage = %+v", extra)
	}
}

// An API key has no plan limits; a plan whose limits couldn't be read says so.
func TestClaudeWithoutPlanLimits(t *testing.T) {
	now := time.Now()
	key := claudeLimits(decodeUsage(t, `{"subscription_type":null,"rate_limits_available":false,"rate_limits":null}`), now)
	if len(key.Windows) != 0 || !strings.Contains(key.Problem, "isn't signed in to a Claude plan") || key.Plan != "" {
		t.Errorf("API key = %+v", key)
	}
	unread := claudeLimits(decodeUsage(t, `{"subscription_type":"pro","rate_limits_available":true,"rate_limits":null}`), now)
	if len(unread.Windows) != 0 || !strings.Contains(unread.Problem, "couldn't read") || unread.Plan != "Pro" {
		t.Errorf("unread = %+v", unread)
	}
}

// fakeClaude writes a stand-in for Claude Code that answers the SDK's
// control requests as the real one does, and notes what it was started with.
func fakeClaude(t *testing.T, body string) (binary, noted string) {
	t.Helper()
	dir := t.TempDir()
	binary = filepath.Join(dir, "claude")
	noted = filepath.Join(dir, "noted")
	script := "#!/bin/sh\necho \"$@\" > '" + noted + "'\npwd >> '" + noted + "'\n" + body
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return binary, noted
}

const answeringClaude = `while IFS= read -r line; do
  case "$line" in
    *'"subtype":"initialize"'*)
      echo '{"type":"system","subtype":"status"}'
      echo '{"type":"control_response","response":{"subtype":"success","request_id":"someone-else","response":{}}}'
      echo '{"type":"control_response","response":{"subtype":"success","request_id":"agentman-init","response":{"commands":[]}}}' ;;
    *'"subtype":"get_usage"'*'"skip_behaviors":true'*|*'"skip_behaviors":true'*'"subtype":"get_usage"'*)
      echo '{"type":"control_response","response":{"subtype":"success","request_id":"agentman-usage","response":{"subscription_type":"max","rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":5,"resets_at":null}}}}}' ;;
  esac
done
`

// Claude Code is started speaking its SDK's protocol with nothing kept or
// run, introduced to, and asked for its usage without the transcript scan.
func TestAskClaudeSpeaksItsSDKProtocol(t *testing.T) {
	binary, noted := fakeClaude(t, answeringClaude)
	dir := t.TempDir()
	usage, err := askClaude(context.Background(), binary, dir, claudeEnv(os.Environ()))
	if err != nil {
		t.Fatal(err)
	}
	if usage.SubscriptionType == nil || *usage.SubscriptionType != "max" || usage.RateLimits == nil ||
		usage.RateLimits.FiveHour == nil || *usage.RateLimits.FiveHour.Utilization != 5 {
		t.Fatalf("usage = %+v", usage)
	}
	data, err := os.ReadFile(noted)
	if err != nil {
		t.Fatal(err)
	}
	started := string(data)
	for _, want := range []string{"--input-format stream-json", "--output-format stream-json", "--no-session-persistence",
		"--setting-sources user", `{"disableAllHooks":true}`, "--strict-mcp-config"} {
		if !strings.Contains(started, want) {
			t.Errorf("started with %q, missing %q", started, want)
		}
	}
	if resolved, _ := filepath.EvalSymlinks(dir); !strings.Contains(started, resolved) {
		t.Errorf("not started in %s: %q", dir, started)
	}
}

// A Claude Code that never answers is given up on, and stopped.
func TestAskClaudeGivesUpOnSilence(t *testing.T) {
	binary, _ := fakeClaude(t, "sleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := askClaude(ctx, binary, t.TempDir(), os.Environ()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("took %v to give up", took)
	}
}

// One that refuses, or leaves, is a failed read.
func TestAskClaudeRefused(t *testing.T) {
	binary, _ := fakeClaude(t, `read -r line
echo '{"type":"control_response","response":{"subtype":"error","request_id":"agentman-init","error":"no"}}'
`)
	if _, err := askClaude(context.Background(), binary, t.TempDir(), os.Environ()); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("refused: err = %v", err)
	}
	gone, _ := fakeClaude(t, "exit 0\n")
	if _, err := askClaude(context.Background(), gone, t.TempDir(), os.Environ()); err == nil {
		t.Error("a Claude Code that left should be a failed read")
	}
}

// The environment Claude Code is started with doesn't say it runs inside
// another Claude Code, and keeps it from updating itself meanwhile.
func TestClaudeEnvironment(t *testing.T) {
	env := claudeEnv([]string{"HOME=/h", "CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "PATH=/bin"})
	if slices.Contains(env, "CLAUDECODE=1") || slices.Contains(env, "CLAUDE_CODE_ENTRYPOINT=cli") {
		t.Errorf("env = %v", env)
	}
	for _, want := range []string{"HOME=/h", "PATH=/bin", "DISABLE_AUTOUPDATER=1", "ENABLE_CLAUDEAI_MCP_SERVERS=false"} {
		if !slices.Contains(env, want) {
			t.Errorf("env %v is missing %s", env, want)
		}
	}
}

// writeRollout writes a Codex rollout under home's sessions, last written at
// written.
func writeRollout(t *testing.T, home, name string, written time.Time, lines ...string) {
	t.Helper()
	path := filepath.Join(home, ".codex", "sessions", "2026", "10", "31", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
}

func tokenCount(at time.Time, rateLimits string) string {
	return `{"timestamp":"` + at.UTC().Format(time.RFC3339Nano) + `","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":` + rateLimits + `}}`
}

// Without Codex's daemon, the newest reply's numbers in the rollouts: Codex's
// own bucket, not another product's, the newest across files.
func TestCodexLimitsFromItsRollouts(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	reply := now.Add(-20 * time.Minute)
	resets := now.Add(2 * time.Hour).Unix()
	codex := `{"limit_id":"codex","plan_type":"plus","primary":{"used_percent":95.0,"window_minutes":300,"resets_at":` +
		strconv.FormatInt(resets, 10) + `},"secondary":{"used_percent":84.0,"window_minutes":10080,"resets_at":` + strconv.FormatInt(now.Add(-time.Minute).Unix(), 10) + `}}`
	writeRollout(t, home, "rollout-new.jsonl", reply,
		tokenCount(reply.Add(-time.Minute), `{"limit_id":"codex","plan_type":"plus","primary":{"used_percent":90.0,"window_minutes":300,"resets_at":1}}`),
		tokenCount(reply, codex),
		`{"timestamp":"`+reply.Format(time.RFC3339)+`","type":"response_item","payload":{"type":"message"}}`,
		tokenCount(reply.Add(time.Second), `{"limit_id":"premium","plan_type":"plus","primary":null,"secondary":null}`))
	writeRollout(t, home, "rollout-old.jsonl", now.Add(-3*time.Hour),
		tokenCount(now.Add(-3*time.Hour), `{"limit_id":"codex","plan_type":"plus","primary":{"used_percent":10.0,"window_minutes":300,"resets_at":1}}`))

	r := NewReader(home)
	r.now = func() time.Time { return now }
	got := r.readCodex(context.Background())
	if got == nil || got.failed {
		t.Fatalf("got %+v", got)
	}
	agent := got.AgentLimits
	if agent.Kind != protocol.KindCodex || agent.Plan != "Plus" || !agent.LastTurn || agent.CheckedAt != reply.UnixMilli() {
		t.Fatalf("agent = %+v", agent)
	}
	if len(agent.Windows) != 2 {
		t.Fatalf("windows = %+v", agent.Windows)
	}
	session, week := agent.Windows[0], agent.Windows[1]
	if session.ID != "primary" || session.Kind != "session" || session.Label != "Session" || session.Used != 95 ||
		session.ResetsAt != resets*1000 || session.Minutes != 300 {
		t.Errorf("session = %+v", session)
	}
	if week.ID != "secondary" || week.Kind != "weekly" || week.Used != 0 || week.ResetsAt != 0 {
		t.Errorf("a week that has reset since is back to nothing: %+v", week)
	}
}

// Older Codex said how long until a reset, not when.
func TestCodexRolloutsCountdown(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	reply := now.Add(-time.Hour)
	writeRollout(t, home, "rollout-a.jsonl", reply, tokenCount(reply,
		`{"primary":{"used_percent":40,"window_minutes":300,"resets_in_seconds":7200},"secondary":null}`))
	snapshot, ok := lastCodexRecord(filepath.Join(home, ".codex"), now)
	if !ok || snapshot.Primary == nil || !snapshot.Primary.ResetsAt.Equal(reply.Add(2*time.Hour)) || snapshot.Secondary != nil {
		t.Errorf("snapshot = %+v", snapshot)
	}
}

// Codex's daemon is asked first; rollouts older than a week aren't read; with
// Codex never used there is nothing to show.
func TestCodexDaemonFirst(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	writeRollout(t, home, "rollout-stale.jsonl", now.AddDate(0, 0, -9), tokenCount(now.AddDate(0, 0, -9),
		`{"limit_id":"codex","primary":{"used_percent":10,"window_minutes":300,"resets_at":1}}`))

	asked := NewReader(home, WithCodexDaemon(func(context.Context) (CodexSnapshot, error) {
		return CodexSnapshot{Plan: "pro", Primary: &CodexWindow{Used: 3}, Secondary: &CodexWindow{Used: 50, Minutes: 10080}}, nil
	}))
	asked.now = func() time.Time { return now }
	agent := asked.readCodex(context.Background()).AgentLimits
	if agent.LastTurn || agent.Plan != "Pro" || agent.CheckedAt != now.UnixMilli() || len(agent.Windows) != 2 ||
		agent.Windows[0].Minutes != 300 || agent.Windows[1].Used != 50 {
		t.Errorf("from the daemon: %+v", agent)
	}

	down := NewReader(home, WithCodexDaemon(func(context.Context) (CodexSnapshot, error) {
		return CodexSnapshot{}, errors.New("down")
	}))
	down.now = func() time.Time { return now }
	stale := down.readCodex(context.Background()).AgentLimits
	if len(stale.Windows) != 0 || !strings.Contains(stale.Problem, "past week") {
		t.Errorf("a stale rollout: %+v", stale)
	}

	if never := NewReader(t.TempDir()).readCodex(context.Background()); never != nil {
		t.Errorf("Codex never used: %+v", never)
	}
}

// A free plan's first window is its month.
func TestCodexFreePlanMonth(t *testing.T) {
	agent := codexLimits(CodexSnapshot{Plan: "free", Primary: &CodexWindow{Used: 1}}, false, time.Now())
	if len(agent.Windows) != 1 || agent.Windows[0].Kind != "monthly" || agent.Windows[0].Label != "Monthly" {
		t.Errorf("free = %+v", agent.Windows)
	}
}

// stubClaude stands in for asking Claude Code, counting the asks; claude must
// be on the PATH for it to be asked at all.
type stubClaude struct {
	asks   atomic.Int32
	mu     sync.Mutex
	answer claudeUsage
	err    error
	gate   chan struct{}
}

func (s *stubClaude) ask(context.Context) (claudeUsage, error) {
	s.asks.Add(1)
	if s.gate != nil {
		<-s.gate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.answer, s.err
}

func withClaudeOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// An answer is given again for a couple of minutes; asking to read again
// reads, unless the last read was moments ago.
func TestReaderKeepsItsAnswer(t *testing.T) {
	withClaudeOnPath(t)
	stub := &stubClaude{answer: decodeUsage(t, sampleUsage)}
	now := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	r := NewReader(t.TempDir())
	r.claude = stub.ask
	r.now = func() time.Time { return now }

	first := r.Read(context.Background(), false)
	if len(first.Agents) != 1 || first.Agents[0].Kind != protocol.KindClaude || len(first.Agents[0].Windows) != 5 {
		t.Fatalf("first = %+v", first)
	}
	r.Read(context.Background(), false)
	now = now.Add(5 * time.Second)
	r.Read(context.Background(), true)
	if n := stub.asks.Load(); n != 1 {
		t.Errorf("asked %d times within moments, want 1", n)
	}
	now = now.Add(soonest)
	r.Read(context.Background(), true)
	if n := stub.asks.Load(); n != 2 {
		t.Errorf("asked %d times after a refresh, want 2", n)
	}
	now = now.Add(keep - time.Second)
	r.Read(context.Background(), false)
	if n := stub.asks.Load(); n != 2 {
		t.Errorf("asked %d times within %v, want 2", n, keep)
	}
	now = now.Add(2 * time.Second)
	r.Read(context.Background(), false)
	if n := stub.asks.Load(); n != 3 {
		t.Errorf("asked %d times after %v, want 3", n, keep)
	}
}

// Callers asking while a read is in flight share it.
func TestReaderSharesARead(t *testing.T) {
	withClaudeOnPath(t)
	stub := &stubClaude{answer: decodeUsage(t, sampleUsage), gate: make(chan struct{})}
	r := NewReader(t.TempDir())
	r.claude = stub.ask
	var wg sync.WaitGroup
	answers := make([]protocol.Limits, 4)
	for i := range answers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			answers[i] = r.Read(context.Background(), true)
		}()
	}
	for stub.asks.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(stub.gate)
	wg.Wait()
	if n := stub.asks.Load(); n != 1 {
		t.Errorf("asked %d times, want 1", n)
	}
	for _, answer := range answers {
		if len(answer.Agents) != 1 {
			t.Errorf("answer = %+v", answer)
		}
	}
}

// A caller that gives up is answered with what there is, and the read
// carries on for the next.
func TestReaderCallerGivesUp(t *testing.T) {
	withClaudeOnPath(t)
	stub := &stubClaude{answer: decodeUsage(t, sampleUsage), gate: make(chan struct{})}
	r := NewReader(t.TempDir())
	r.claude = stub.ask
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if early := r.Read(ctx, false); early.Agents == nil || len(early.Agents) != 0 {
		t.Errorf("early = %+v, want an empty list", early)
	}
	close(stub.gate)
	if later := r.Read(context.Background(), false); len(later.Agents) != 1 || stub.asks.Load() != 1 {
		t.Errorf("later = %+v after %d asks", later, stub.asks.Load())
	}
}

// When Claude Code can't be asked, its last numbers are shown with why; an
// answer of no limits clears them.
func TestReaderShowsTheLastGoodNumbers(t *testing.T) {
	withClaudeOnPath(t)
	stub := &stubClaude{answer: decodeUsage(t, sampleUsage)}
	now := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	r := NewReader(t.TempDir())
	r.claude = stub.ask
	r.now = func() time.Time { return now }
	good := r.Read(context.Background(), false).Agents[0]

	stub.err = errors.New("offline")
	now = now.Add(keep)
	failed := r.Read(context.Background(), false).Agents[0]
	if len(failed.Windows) != len(good.Windows) || failed.CheckedAt != good.CheckedAt ||
		!strings.Contains(failed.Problem, "didn't say") {
		t.Errorf("failed = %+v", failed)
	}

	stub.err = nil
	stub.answer = decodeUsage(t, `{"rate_limits_available":false}`)
	now = now.Add(keep)
	key := r.Read(context.Background(), false).Agents[0]
	stub.err = errors.New("offline")
	now = now.Add(keep)
	after := r.Read(context.Background(), false).Agents[0]
	if len(key.Windows) != 0 || len(after.Windows) != 0 {
		t.Errorf("no limits, then a failure: %+v, %+v", key, after)
	}
}

// Claude Code isn't asked when it isn't installed.
func TestReaderLeavesOutWhatIsNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	stub := &stubClaude{answer: decodeUsage(t, sampleUsage)}
	r := NewReader(t.TempDir())
	r.claude = stub.ask
	if answer := r.Read(context.Background(), false); len(answer.Agents) != 0 || stub.asks.Load() != 0 {
		t.Errorf("answer = %+v after %d asks", answer, stub.asks.Load())
	}
}
