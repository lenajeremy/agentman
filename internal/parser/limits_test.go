package parser

import (
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// claudeRefusal is the line Claude Code writes when the plan has run out,
// as it does on this machine.
func claudeRefusal(ts, text string) string {
	return `{"type":"assistant","uuid":"u-limit","timestamp":"` + ts + `","isApiErrorMessage":true,` +
		`"error":"rate_limit","apiErrorStatus":429,"message":{"model":"<synthetic>","role":"assistant",` +
		`"content":[{"type":"text","text":"` + text + `"}]}}`
}

// codexRefusal is the end of a Codex turn refused at the plan's limit.
func codexRefusal(ts, message string) string {
	return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1",` +
		`"last_agent_message":null,"error":{"message":"` + message + `","codex_error_info":"usage_limit_exceeded"}}}`
}

func ms(t *testing.T, value string) int64 {
	t.Helper()
	when, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return when.UnixMilli()
}

func TestClaudeLimitLineReadsTheRefusal(t *testing.T) {
	limit, decides := ClaudeLimitLine(claudeRefusal("2026-11-18T15:02:00.000Z",
		"You've hit your session limit · resets 5:20pm (Europe/Dublin)"))
	if !decides || limit == nil {
		t.Fatalf("got %v, %v", limit, decides)
	}
	if limit.Text != "You've hit your session limit · resets 5:20pm (Europe/Dublin)" {
		t.Errorf("text %q", limit.Text)
	}
	if limit.At != ms(t, "2026-11-18T15:02:00Z") {
		t.Errorf("at %d", limit.At)
	}
	// Dublin is on UTC in November.
	if want := ms(t, "2026-11-18T17:20:00Z"); limit.ResetsAt != want {
		t.Errorf("resets at %v, want %v", time.UnixMilli(limit.ResetsAt).UTC(), time.UnixMilli(want).UTC())
	}
}

func TestClaudeResetTimes(t *testing.T) {
	cases := []struct {
		name, at, text, want string
	}{
		{"later today", "2026-11-18T09:00:00Z", "You've hit your session limit · resets 1pm (Europe/Dublin)", "2026-11-18T13:00:00Z"},
		{"past today is tomorrow", "2026-11-18T22:30:00Z", "You've hit your 5-hour limit · resets 2:40am (Europe/Dublin)", "2026-11-19T02:40:00Z"},
		{"in summer time", "2026-07-02T09:00:00Z", "You've hit your session limit · resets 11:50pm (Europe/Dublin)", "2026-07-02T22:50:00Z"},
		{"a date", "2026-11-05T10:00:00Z", "You've hit your weekly limit · resets Nov 9 at 11pm (Europe/Dublin)", "2026-11-09T23:00:00Z"},
		{"a date in the new year", "2026-12-30T10:00:00Z", "You've hit your weekly limit · resets Jan 2 at 9am (Europe/Dublin)", "2027-01-02T09:00:00Z"},
		{"another zone", "2026-11-18T09:00:00Z", "You've hit your session limit · resets 5pm (America/New_York)", "2026-11-18T22:00:00Z"},
		{"noon", "2026-11-18T09:00:00Z", "You've hit your session limit · resets 12pm (UTC)", "2026-11-18T12:00:00Z"},
		{"midnight", "2026-11-18T09:00:00Z", "You've hit your session limit · resets 12am (UTC)", "2026-11-19T00:00:00Z"},
	}
	for _, c := range cases {
		got := claudeResetsAt(c.text, ms(t, c.at))
		if want := ms(t, c.want); got != want {
			t.Errorf("%s: %v, want %v", c.name, time.UnixMilli(got).UTC(), time.UnixMilli(want).UTC())
		}
	}
	// No time to read: kept until the next turn instead.
	if got := claudeResetsAt("You're out of usage credits. Run /usage-credits to keep using Fable 5 or /model to switch models.", ms(t, "2026-11-18T09:00:00Z")); got != 0 {
		t.Errorf("a refusal without a time reset at %d", got)
	}
}

func TestClaudeLimitLineSettlesOnLaterLines(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		decides bool
	}{
		{"a prompt", `{"type":"user","message":{"role":"user","content":"carry on"}}`, true},
		{"a tool result", `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"ok"}]}}`, true},
		{"a reply", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done"}]}}`, true},
		{"a meta line", `{"type":"user","isMeta":true,"message":{"role":"user","content":"caveat"}}`, false},
		{"bookkeeping", `{"type":"last-prompt","lastPrompt":"carry on"}`, false},
		{"a queue operation", `{"type":"queue-operation","operation":"enqueue"}`, false},
	}
	for _, c := range cases {
		limit, decides := ClaudeLimitLine(c.line)
		if limit != nil || decides != c.decides {
			t.Errorf("%s: %v, %v; want nil, %v", c.name, limit, decides, c.decides)
		}
	}
}

// A key on the API that is only throttled passes in a moment: an error, not
// a plan out of usage.
func TestClaudeThrottledKeyIsNotALimit(t *testing.T) {
	line := claudeRefusal("2026-11-18T15:02:00.000Z", "API Error: Request rejected (429) · Rate limited")
	if limit, decides := ClaudeLimitLine(line); limit != nil || !decides {
		t.Fatalf("got %v, %v", limit, decides)
	}
	rows := NewClaudeParser("claude:s").Parse(line, 0)
	if len(rows) != 1 || rows[0].Notice != protocol.NoticeError || rows[0].Role != protocol.RoleSystem {
		t.Fatalf("rows %+v", rows)
	}
}

func TestClaudeRefusalIsANotice(t *testing.T) {
	rows := NewClaudeParser("claude:s").Parse(claudeRefusal("2026-11-18T15:02:00.000Z",
		"You've hit your session limit · resets 5:20pm (Europe/Dublin)"), 0)
	if len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}
	row := rows[0]
	if row.Role != protocol.RoleSystem || row.Notice != protocol.NoticeLimit || row.ID != "u-limit" ||
		row.Text != "You've hit your session limit · resets 5:20pm (Europe/Dublin)" {
		t.Errorf("row %+v", row)
	}
	// Any other refusal is an error notice, not the agent's words.
	other := `{"type":"assistant","uuid":"u2","isApiErrorMessage":true,"error":"server_error",` +
		`"message":{"content":[{"type":"text","text":"API Error: 500 Internal server error"}]}}`
	if rows := NewClaudeParser("claude:s").Parse(other, 0); len(rows) != 1 || rows[0].Notice != protocol.NoticeError {
		t.Errorf("rows %+v", rows)
	}
	// An ordinary reply is untouched.
	reply := `{"type":"assistant","uuid":"u3","message":{"content":[{"type":"text","text":"Done"}]}}`
	if rows := NewClaudeParser("claude:s").Parse(reply, 0); len(rows) != 1 || rows[0].Notice != "" || rows[0].Role != protocol.RoleAssistant {
		t.Errorf("rows %+v", rows)
	}
}

func TestCodexLimitLineReadsTheRefusal(t *testing.T) {
	line := codexRefusal("2026-11-18T17:06:20.100Z", "You’ve hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), "+
		"visit https://chatgpt.com/settings/usage to purchase more credits or try again at 7:19 PM.")
	limit, decides := CodexLimitLine(line)
	if !decides || limit == nil {
		t.Fatalf("got %v, %v", limit, decides)
	}
	if limit.Text != "You’ve hit your usage limit · try again at 7:19 PM" {
		t.Errorf("text %q", limit.Text)
	}
	at := time.UnixMilli(ms(t, "2026-11-18T17:06:20Z")).In(time.Local)
	want := time.Date(at.Year(), at.Month(), at.Day(), 19, 19, 0, 0, time.Local)
	if want.Before(at) {
		want = want.AddDate(0, 0, 1)
	}
	if limit.ResetsAt != want.UnixMilli() {
		t.Errorf("resets at %v, want %v", time.UnixMilli(limit.ResetsAt), want)
	}

	// A date and year, when the plan is out for days.
	dated := codexRefusal("2026-08-18T10:00:00.000Z", "You've hit your usage limit. Visit https://chatgpt.com/codex/settings/usage "+
		"to purchase more credits or try again at Aug 21st, 2026 7:19 PM.")
	limit, _ = CodexLimitLine(dated)
	if want := time.Date(2026, 8, 21, 19, 19, 0, 0, time.Local).UnixMilli(); limit == nil || limit.ResetsAt != want {
		t.Errorf("dated: %+v", limit)
	}
	if limit.Text != "You've hit your usage limit · try again at Aug 21st, 2026 7:19 PM" {
		t.Errorf("dated text %q", limit.Text)
	}
}

func TestCodexLimitLineSettlesOnTurns(t *testing.T) {
	started := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"t2"}}`
	if limit, decides := CodexLimitLine(started); limit != nil || !decides {
		t.Errorf("a turn starting: %v, %v", limit, decides)
	}
	done := `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"t2","last_agent_message":"Done"}}`
	if limit, decides := CodexLimitLine(done); limit != nil || !decides {
		t.Errorf("a turn done: %v, %v", limit, decides)
	}
	overloaded := `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"t3","error":{"message":"Selected model is at capacity. Please try a different model.","codex_error_info":"server_overloaded"}}}`
	if limit, decides := CodexLimitLine(overloaded); limit != nil || !decides {
		t.Errorf("a model at capacity: %v, %v", limit, decides)
	}
	if _, decides := CodexLimitLine(`{"type":"event_msg","payload":{"type":"token_count"}}`); decides {
		t.Error("a token count settles it")
	}
}

// A turn that failed showed nothing at all: the thread ended at the user's
// message, as if Codex were still thinking.
func TestCodexFailedTurnIsANotice(t *testing.T) {
	p := NewCodexParser("codex:s")
	rows := p.Parse(codexRefusal("2026-11-18T17:06:20.100Z", "You’ve hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro) or try again at 7:19 PM."), 0)
	if len(rows) != 1 || rows[0].Notice != protocol.NoticeLimit || rows[0].Role != protocol.RoleSystem ||
		rows[0].ID != "codex-failed:t1" || rows[0].Text != "You’ve hit your usage limit · try again at 7:19 PM" {
		t.Fatalf("rows %+v", rows)
	}
	overloaded := `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"t3","error":{"message":"Selected model is at capacity. Please try a different model.","codex_error_info":"server_overloaded"}}}`
	rows = p.Parse(overloaded, 10)
	if len(rows) != 1 || rows[0].Notice != protocol.NoticeError || rows[0].Text != "Selected model is at capacity. Please try a different model." {
		t.Fatalf("rows %+v", rows)
	}
	done := `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"t2","last_agent_message":"Done"}}`
	if rows := p.Parse(done, 20); len(rows) != 0 {
		t.Fatalf("a turn that went well made rows %+v", rows)
	}
}

func TestLimitCurrentEndsAtTheReset(t *testing.T) {
	limit := &protocol.Limit{Text: "x", At: 1000, ResetsAt: 5000}
	if LimitCurrent(limit, time.UnixMilli(4999)) == nil {
		t.Error("over before the reset")
	}
	if LimitCurrent(limit, time.UnixMilli(5000)) != nil {
		t.Error("still out at the reset")
	}
	unknown := &protocol.Limit{Text: "x", At: 1000}
	if LimitCurrent(unknown, time.UnixMilli(1<<50)) == nil {
		t.Error("a limit with no reset time ended by itself")
	}
}

// However the JSON is spaced.
func TestClaudeLimitLineReadsSpacedJSON(t *testing.T) {
	line := `{"type": "assistant", "isApiErrorMessage": true, "error": "rate_limit", ` +
		`"message": {"content": [{"type": "text", "text": "You've hit your session limit"}]}}`
	if limit, decides := ClaudeLimitLine(line); limit == nil || !decides {
		t.Fatalf("got %v, %v", limit, decides)
	}
}

func TestIsUsageLimit(t *testing.T) {
	for _, text := range []string{
		"You've hit your usage limit Get Cursor Pro for more Agent usage, unlimited Tab, and more.",
		"You've hit your session limit · resets 5:20pm (Europe/Dublin)",
		"You've reached your Fable 5 limit. Run /usage-credits to continue or switch models with /model.",
		"You're out of usage credits. Run /usage-credits to keep using Fable 5",
		"You have reached your monthly usage limit",
		"Quota exceeded for this billing period",
		"Insufficient credits to continue",
		"You've used up your plan's allowance",
		"Monthly request limit reached",
		"Internal error: MonthlyLimitReached",
	} {
		if !IsUsageLimit(text) {
			t.Errorf("not a limit: %q", text)
		}
	}
	for _, text := range []string{
		"Rate limit exceeded, retrying in 5s",
		"You have exceeded the rate limit for this endpoint",
		"API Error: Request rejected (429) · Rate limited",
		"Selected model is at capacity. Please try a different model.",
		"User aborted request",
		"the limit of 10 files was reached",
		"Concurrency Limit reached",
	} {
		if IsUsageLimit(text) {
			t.Errorf("a limit: %q", text)
		}
	}
}

func TestLimitSentence(t *testing.T) {
	cases := map[string]string{
		"You've hit your usage limit Get Cursor Pro for more Agent usage, unlimited Tab, and more.": "You've hit your usage limit",
		"You've reached your Fable 5 limit. Run /usage-credits to continue":                         "You've reached your Fable 5 limit",
		"Quota exceeded for this billing period":                                                    "Quota exceeded for this billing period",
	}
	for in, want := range cases {
		if got := limitSentence(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestFailureNotice(t *testing.T) {
	text, notice := FailureNotice("Cursor", "Internal error: You've hit your usage limit Get Cursor Pro for more Agent usage.")
	if text != "You've hit your usage limit" || notice != protocol.NoticeLimit {
		t.Errorf("a limit: %q %q", text, notice)
	}
	text, notice = FailureNotice("Kiro", "Kiro ACP process exited")
	if text != "Kiro stopped: Kiro ACP process exited" || notice != protocol.NoticeError {
		t.Errorf("an error: %q %q", text, notice)
	}
}

func TestLimitFromRows(t *testing.T) {
	limit := protocol.Message{Role: protocol.RoleSystem, Notice: protocol.NoticeLimit, Text: "You've hit your usage limit", Ts: 9}
	user := protocol.Message{Role: protocol.RoleUser, Text: "go on"}
	plain := protocol.Message{Role: protocol.RoleSystem, Text: "Interrupted"}
	if got := LimitFromRows([]protocol.Message{user, limit}); got == nil || got.Text != "You've hit your usage limit" || got.At != 9 {
		t.Errorf("ended at the limit: %+v", got)
	}
	if got := LimitFromRows([]protocol.Message{user, limit, plain}); got == nil {
		t.Error("a notice after it hid the limit")
	}
	if got := LimitFromRows([]protocol.Message{limit, user}); got != nil {
		t.Errorf("a prompt after the limit: %+v", got)
	}
	if got := LimitFromRows(nil); got != nil {
		t.Errorf("no rows: %+v", got)
	}
}

func TestMuseTurnAtTheLimit(t *testing.T) {
	end := MuseTurnEnd{TurnID: "t1", Terminal: "failed", Reason: "usage_limit"}
	end.Error = &struct {
		Message string `json:"message"`
	}{Message: "This turn stopped because your usage limit was reached."}
	row, ok := MuseTurnEnded("muse:s", end, 5)
	if !ok || row.Notice != protocol.NoticeLimit || row.Text != "This turn stopped because your usage limit was reached" {
		t.Fatalf("row %+v", row)
	}
	if limit := LimitFromRows([]protocol.Message{row}); limit == nil || limit.At != 5 {
		t.Fatalf("limit %+v", limit)
	}
	// The reason alone.
	row, _ = MuseTurnEnded("muse:s", MuseTurnEnd{TurnID: "t2", Terminal: "failed", Reason: "usage_limit"}, 5)
	if row.Notice != protocol.NoticeLimit {
		t.Fatalf("reason only: %+v", row)
	}
	row, _ = MuseTurnEnded("muse:s", MuseTurnEnd{TurnID: "t3", Terminal: "failed", Reason: "provider_unavailable"}, 5)
	if row.Notice != protocol.NoticeError || row.Text != "Muse turn failed: provider_unavailable" {
		t.Fatalf("an error: %+v", row)
	}
}
