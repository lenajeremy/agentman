package parser

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Usage limits: an agent whose plan has run out ends its turn with a line
// saying so, and says nothing more until the plan resets. Read here so the
// apps can say the agent is out of usage, rather than leave a quiet thread
// that looks like one still thinking.
//
// Claude Code writes the refusal as an assistant record of its own making:
//
//	{"type":"assistant","isApiErrorMessage":true,"error":"rate_limit",
//	 "message":{"content":[{"type":"text","text":"You've hit your session limit · resets 5:20pm (Europe/Dublin)"}]}}
//
// Codex ends the turn with the error on its task_complete:
//
//	{"type":"event_msg","payload":{"type":"task_complete","error":{
//	 "message":"You've hit your usage limit. Upgrade to Pro (…) or try again at 7:19 PM.",
//	 "codex_error_info":"usage_limit_exceeded"}}}

// codexUsageLimit is Codex's codex_error_info for a plan that ran out.
const codexUsageLimit = "usage_limit_exceeded"

// ClaudeLimitLine reads what one Claude transcript line says about the
// plan's limit. decides is true for a line that settles whether the agent is
// out of usage now: any assistant record, and any of the user's, since a
// prompt or a tool's result means a turn is under way. limit is set when the
// line is the refusal itself.
func ClaudeLimitLine(line string) (limit *protocol.Limit, decides bool) {
	// Most lines are neither; skip decoding them.
	if !strings.Contains(line, `"assistant"`) && !strings.Contains(line, `"user"`) {
		return nil, false
	}
	var rec claudeRecord
	if !decode(line, &rec) || rec.Message == nil {
		return nil, false
	}
	switch rec.Type {
	case "user":
		if rec.IsMeta {
			return nil, false
		}
		return nil, true
	case "assistant":
		if !claudeHitLimit(rec) {
			return nil, true
		}
		return claudeLimit(rec), true
	}
	return nil, false
}

// claudeAPIError is the error Claude Code recorded in place of a reply, or
// "" for a reply.
func claudeAPIError(rec claudeRecord) string {
	if !rec.IsAPIErrorMessage {
		return ""
	}
	var kind string
	if json.Unmarshal(rec.Error, &kind) != nil || kind == "" {
		return "error"
	}
	return kind
}

// claudeHitLimit is a refusal because the plan ran out. A key on the API
// that is only being throttled is refused as rate_limit too, but Claude Code
// words that as an API error ("API Error: Request rejected (429)"), and it
// passes in a moment; that one is an error, not a limit.
func claudeHitLimit(rec claudeRecord) bool {
	return rec.Type == "assistant" && claudeAPIError(rec) == "rate_limit" &&
		!strings.HasPrefix(claudeRecordText(rec), "API Error")
}

func claudeLimit(rec claudeRecord) *protocol.Limit {
	text := firstLine(claudeRecordText(rec))
	if text == "" {
		text = "You've hit your usage limit"
	}
	at := parseTime(rec.Timestamp)
	return &protocol.Limit{Text: text, At: at, ResetsAt: claudeResetsAt(text, at)}
}

// claudeRecordText is the text of a record's message.
func claudeRecordText(rec claudeRecord) string {
	if rec.Message == nil {
		return ""
	}
	var plain string
	if json.Unmarshal(rec.Message.Content, &plain) == nil {
		return strings.TrimSpace(plain)
	}
	var blocks []claudeBlock
	if json.Unmarshal(rec.Message.Content, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.Join(parts, "\n")
}

// codexTurnError is the error a Codex turn ended with.
type codexTurnError struct {
	Message string `json:"message"`
	Info    string `json:"codex_error_info"`
}

// codexEndError is the error a task_complete record carries, or nil.
func codexEndError(payload *codexPayload) *codexTurnError {
	if payload == nil || payload.Type != "task_complete" || len(payload.Error) == 0 {
		return nil
	}
	var failed codexTurnError
	if json.Unmarshal(payload.Error, &failed) != nil || strings.TrimSpace(failed.Message) == "" {
		return nil
	}
	return &failed
}

// CodexLimitLine reads what one rollout line says about the plan's limit:
// a turn starting settles that the agent isn't out of usage, and a turn
// ending settles it either way.
func CodexLimitLine(line string) (limit *protocol.Limit, decides bool) {
	if !strings.Contains(line, `"task_started"`) && !strings.Contains(line, `"task_complete"`) {
		return nil, false
	}
	var rec codexRecord
	if !decode(line, &rec) || rec.Type != "event_msg" || rec.Payload == nil {
		return nil, false
	}
	switch rec.Payload.Type {
	case "task_started":
		return nil, true
	case "task_complete":
		failed := codexEndError(rec.Payload)
		if failed == nil || failed.Info != codexUsageLimit {
			return nil, true
		}
		return codexLimit(failed.Message, parseTime(rec.Timestamp)), true
	}
	return nil, false
}

func codexLimit(message string, at int64) *protocol.Limit {
	return &protocol.Limit{Text: codexLimitText(message), At: at, ResetsAt: codexResetsAt(message, at)}
}

// codexLimitText is Codex's refusal in one line: its first sentence and when
// to try again, without the links to upgrade.
func codexLimitText(message string) string {
	message = firstLine(message)
	first := message
	if end := strings.Index(message, ". "); end > 0 {
		first = message[:end]
	}
	first = strings.TrimSuffix(strings.TrimSpace(first), ".")
	if when := codexReset.FindString(message); when != "" {
		return first + " · " + strings.TrimSuffix(when, ".")
	}
	return first
}

// LimitCurrent is limit while it still holds at now: nil once the time the
// agent said it resets has passed.
func LimitCurrent(limit *protocol.Limit, now time.Time) *protocol.Limit {
	if limit == nil || (limit.ResetsAt > 0 && now.UnixMilli() >= limit.ResetsAt) {
		return nil
	}
	return limit
}

var (
	// "resets 5:20pm (Europe/Dublin)", "resets 11pm (Europe/Dublin)",
	// "resets Nov 9 at 11pm (Europe/Dublin)".
	claudeReset = regexp.MustCompile(`(?i)resets\s+(?:([a-z]{3,9})\s+(\d{1,2})\s+at\s+)?(\d{1,2})(?::(\d{2}))?\s*([ap]m)(?:\s*\(([^)\s]+)\))?`)
	// "try again at 7:19 PM", "try again at Aug 21st, 2026 7:19 PM".
	codexReset = regexp.MustCompile(`(?i)try again at\s+(?:([a-z]{3,9})\s+(\d{1,2})(?:st|nd|rd|th)?,?\s*(\d{4})?\s+)?(\d{1,2})(?::(\d{2}))?\s*([ap]m)`)
)

// claudeResetsAt is when a Claude refusal says the limit resets, in the zone
// it names, in Unix milliseconds; 0 when it says no time.
func claudeResetsAt(text string, at int64) int64 {
	m := claudeReset.FindStringSubmatch(text)
	if m == nil {
		return 0
	}
	zone := time.Local
	if m[6] != "" {
		if loaded, err := time.LoadLocation(m[6]); err == nil {
			zone = loaded
		}
	}
	return resetTime(at, zone, m[1], m[2], "", m[3], m[4], m[5])
}

// codexResetsAt is when a Codex refusal says to try again. Codex writes the
// time in the Mac's own zone.
func codexResetsAt(text string, at int64) int64 {
	m := codexReset.FindStringSubmatch(text)
	if m == nil {
		return 0
	}
	return resetTime(at, time.Local, m[1], m[2], m[3], m[4], m[5], m[6])
}

// resetTime is the first moment at or after the hit that matches what the
// agent wrote: a clock time alone is the next one, and a date without a year
// is the next one too.
func resetTime(at int64, zone *time.Location, month, day, year, hour, minute, meridiem string) int64 {
	if at <= 0 {
		at = time.Now().UnixMilli()
	}
	ref := time.UnixMilli(at).In(zone)
	h, err := strconv.Atoi(hour)
	if err != nil || h < 1 || h > 12 {
		return 0
	}
	mins := 0
	if minute != "" {
		if mins, err = strconv.Atoi(minute); err != nil || mins > 59 {
			return 0
		}
	}
	h %= 12
	if strings.EqualFold(meridiem, "pm") {
		h += 12
	}
	if month == "" {
		when := time.Date(ref.Year(), ref.Month(), ref.Day(), h, mins, 0, 0, zone)
		if when.Before(ref) {
			when = when.AddDate(0, 0, 1)
		}
		return when.UnixMilli()
	}
	named, err := time.Parse("Jan", strings.ToUpper(month[:1])+strings.ToLower(month[1:min(3, len(month))]))
	if err != nil {
		return 0
	}
	d, err := strconv.Atoi(day)
	if err != nil || d < 1 || d > 31 {
		return 0
	}
	y := ref.Year()
	if year != "" {
		if y, err = strconv.Atoi(year); err != nil {
			return 0
		}
	}
	when := time.Date(y, named.Month(), d, h, mins, 0, 0, zone)
	if year == "" && when.Before(ref.AddDate(0, 0, -1)) {
		when = when.AddDate(1, 0, 0)
	}
	return when.UnixMilli()
}

// usageLimitWords are how agents word a plan that has run out, for those that
// give no code for it: Cursor's server ("You've hit your usage limit Get
// Cursor Pro …"), Kiro's, OpenCode's providers. A throttle that passes in a
// moment ("rate limit exceeded, retrying") is not among them.
var usageLimitWords = regexp.MustCompile(`(?i)\b(?:hit|reached|exceeded|used up)\s+(?:your|the)\s+(?:[\w.'’-]+\s+){0,3}(?:usage|spend(?:ing)?|plan|monthly|weekly|daily|session|quota|credit)?\s*(?:limit|quota|cap|allowance)\b|\bout of (?:usage|credits|quota)\b|\b(?:usage|quota|credit) (?:limit )?(?:exceeded|exhausted|reached)\b|\binsufficient (?:credits|quota|balance|funds)\b|\bcredit balance is too low\b|\b(?:monthly|daily|weekly|usage|request|credit)\s+(?:request\s+|usage\s+|credit\s+)?limit\s+(?:reached|exceeded|hit)\b|\b(?:monthly|daily|weekly)limitreached\b`)

// IsUsageLimit reports whether an agent's error says its plan ran out.
func IsUsageLimit(text string) bool {
	match := usageLimitWords.FindString(text)
	return match != "" && !strings.Contains(strings.ToLower(match), "rate")
}

// upsellStart is a capital starting a new sentence with no full stop before it.
var upsellStart = regexp.MustCompile(`^\s+[A-Z]`)

// limitSentence is an agent's refusal in one short line: up to the first
// sentence's end, or where an upsell starts on the same line ("… limit Get
// Cursor Pro for more"), without links.
func limitSentence(text string) string {
	text = firstLine(text)
	// The sentence the limit is in, without what an error wrapped it in
	// ("session/prompt: You've hit …").
	if m := usageLimitWords.FindStringIndex(text); m != nil {
		start := max(strings.LastIndex(text[:m[0]], ". "), strings.LastIndex(text[:m[0]], ": "))
		if start >= 0 {
			text = text[start+2:]
		}
	}
	if end := strings.Index(text, ". "); end > 0 {
		text = text[:end]
	}
	if m := usageLimitWords.FindStringIndex(text); m != nil {
		// Words after the match that start a new sentence without a full
		// stop: a capital after the limit's last word.
		rest := text[m[1]:]
		if upsellStart.MatchString(rest) {
			text = text[:m[1]]
		}
	}
	return strings.TrimSuffix(strings.TrimSpace(text), ".")
}

// CursorTurnNotice is the row for a Cursor turn that ended without success:
// the plan's limit, another error, or the user stopping it. ok is false for
// a turn that went well.
func CursorTurnNotice(status, failure string) (text, notice string, ok bool) {
	failure = strings.TrimSpace(failure)
	switch {
	case status == "" || status == "success":
		return "", "", false
	case status == "aborted" || strings.EqualFold(failure, "User aborted request"):
		return "Interrupted", "", true
	case IsUsageLimit(failure):
		return limitSentence(failure), protocol.NoticeLimit, true
	case failure == "":
		return "Cursor stopped: " + status, protocol.NoticeError, true
	}
	return failure, protocol.NoticeError, true
}

// CursorLimitLine reads what one Cursor transcript line says about the
// plan's limit. Its records carry no time; the caller fills At.
func CursorLimitLine(line string) (*protocol.Limit, bool) {
	var rec cursorRecord
	if !decode(line, &rec) {
		return nil, false
	}
	if rec.Role == "user" || rec.Role == "assistant" {
		return nil, true
	}
	if rec.Role != "" || rec.Type != "turn_ended" {
		return nil, false
	}
	if text, notice, _ := CursorTurnNotice(rec.Status, rec.Error); notice == protocol.NoticeLimit {
		return &protocol.Limit{Text: text}, true
	}
	return nil, true
}

// FailureNotice is the row for a turn an agent's service refused, from the
// error it gave: the plan's limit, said in the agent's words, or another
// error, said as "Cursor stopped: …".
func FailureNotice(agent, failure string) (text, notice string) {
	failure = strings.TrimSpace(failure)
	if IsUsageLimit(failure) {
		text = limitSentence(failure)
		// A code, not words ("MonthlyLimitReached").
		if !strings.Contains(text, " ") {
			text = "You've hit your usage limit"
		}
		return text, protocol.NoticeLimit
	}
	return agent + " stopped: " + failure, protocol.NoticeError
}

// LimitFromRows is a session's limit as its newest rows say it: set when a
// limit notice comes after the last thing the user or the agent did. For
// sessions whose rows Agentman holds itself (an agent it runs over ACP or
// MSP) rather than reads from a transcript.
func LimitFromRows(rows []protocol.Message) *protocol.Limit {
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.Role == protocol.RoleSystem {
			if row.Notice == protocol.NoticeLimit {
				return &protocol.Limit{Text: row.Text, At: row.Ts}
			}
			continue
		}
		return nil
	}
	return nil
}
