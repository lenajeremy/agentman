// Package hook receives lifecycle events from the agent CLIs.
//
// Polling a transcript tells you a file grew. It cannot tell you *why* a turn
// ended — whether the agent finished, or stopped to ask permission. Hooks
// answer that exactly and immediately, which is what the whole notification
// feature depends on: "your agent is done" has to arrive when the agent is
// done, not up to a second later and possibly wrong.
//
// Claude Code uses lifecycle hooks with JSON on stdin. Codex exposes a
// narrower `notify` callback after a completed turn and appends a different
// JSON shape to argv. Both are normalized here, but their capabilities must
// not be treated as interchangeable.
package hook

import (
	"encoding/json"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Name is a normalized hook lifecycle event. These are Claude's PascalCase
// names; Codex's sole completion notification is mapped to NameStop.
type Name string

const (
	// NameSessionStart fires when a session begins.
	NameSessionStart Name = "SessionStart"
	// NameUserPromptSubmit fires when the user submits a prompt, so the
	// session can flip to busy without waiting for a poll.
	NameUserPromptSubmit Name = "UserPromptSubmit"
	// NameNotification fires when the agent is blocked and wants the user —
	// most usefully, a permission prompt. This is what drives waiting_input.
	NameNotification Name = "Notification"
	// NameStop fires when a turn ends. This is the signal the phone rings on.
	NameStop Name = "Stop"
	// NameSessionEnd fires when a session exits.
	NameSessionEnd Name = "SessionEnd"
)

// Installed is the set of events we register. Deliberately small: every hook
// runs in the agent's critical path, so we subscribe only to what changes what
// the user sees.
var Installed = []Name{
	NameSessionStart,
	NameUserPromptSubmit,
	NameNotification,
	NameStop,
	NameSessionEnd,
}

// ClaudeEventKey maps an event to its key in ~/.claude/settings.json.
func ClaudeEventKey(n Name) string { return string(n) }

// Payload is the JSON an agent CLI writes to a hook's stdin.
//
// Field names are verified against the Claude Code binary: a common set
// (session_id, transcript_path, cwd, permission_mode) plus per-event extras.
// Unknown fields are ignored rather than rejected, so a CLI adding one does
// not break delivery.
type Payload struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	PermissionMode string `json:"permission_mode"`

	// Stop only. LastAssistantMessage is what makes a notification worth
	// reading — it means the phone can show what the agent actually said
	// without opening the session or fetching anything.
	StopHookActive       bool   `json:"stop_hook_active"`
	LastAssistantMessage string `json:"last_assistant_message"`

	// Notification only: why the agent wants attention.
	Message string `json:"message"`

	// UserPromptSubmit only.
	Prompt string `json:"prompt"`
}

// Event is a normalized hook delivery, tagged with which agent sent it.
type Event struct {
	Kind protocol.Kind `json:"kind"`
	Name Name          `json:"name"`
	// SessionID is the composite protocol ID ("claude:<uuid>"), so consumers
	// can match it against a discovered session without knowing the source.
	SessionID string  `json:"sessionId"`
	Payload   Payload `json:"payload"`
	// ReceivedAt is when the daemon got it, in epoch milliseconds.
	ReceivedAt int64 `json:"receivedAt"`
}

// State returns the session state this event implies, and whether it implies
// one at all. SessionStart and SessionEnd deliberately do not: discovery owns
// a session's existence, and a hook racing it would make sessions flicker.
func (e Event) State() (protocol.State, bool) {
	switch e.Name {
	case NameUserPromptSubmit:
		return protocol.StateBusy, true
	case NameNotification:
		// The agent is blocked on the user. More actionable than "done", and
		// the reason this event is worth subscribing to at all.
		return protocol.StateWaitingInput, true
	case NameStop:
		return protocol.StateIdle, true
	default:
		return "", false
	}
}

// IsTurnComplete reports whether this event should ring the user's phone.
//
// StopHookActive means the CLI is re-running the hook after a previous block,
// so the turn is not genuinely finished — notifying there would produce a
// second alert for one piece of work.
func (e Event) IsTurnComplete() bool {
	return e.Name == NameStop && !e.Payload.StopHookActive
}

// Preview is a short summary suitable for a notification body.
func (e Event) Preview() string {
	text := e.Payload.LastAssistantMessage
	if text == "" {
		text = e.Payload.Message
	}
	return clip(text, 180)
}

func clip(text string, maxLen int) string {
	flat := strings.Join(strings.Fields(text), " ")
	runes := []rune(flat)
	if len(runes) <= maxLen {
		return flat
	}
	return string(runes[:maxLen-1]) + "…"
}

// ParsePayload decodes hook stdin, tolerating trailing whitespace and an empty
// body. A malformed payload must never crash the agent's hook call.
func ParsePayload(raw []byte) (Payload, error) {
	var p Payload
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return p, nil
	}
	if err := json.Unmarshal([]byte(trimmed), &p); err != nil {
		return p, err
	}
	if p.SessionID != "" {
		return p, nil
	}

	// Codex's supported `notify` command appends a legacy JSON object as its
	// final argv entry rather than writing Claude's hook shape on stdin.
	var codex struct {
		Type                 string `json:"type"`
		ThreadID             string `json:"thread-id"`
		Cwd                  string `json:"cwd"`
		LastAssistantMessage string `json:"last-assistant-message"`
	}
	if err := json.Unmarshal([]byte(trimmed), &codex); err != nil {
		return p, err
	}
	if codex.Type == "agent-turn-complete" && codex.ThreadID != "" {
		p.SessionID = codex.ThreadID
		p.Cwd = codex.Cwd
		p.HookEventName = string(NameStop)
		p.LastAssistantMessage = codex.LastAssistantMessage
	}
	return p, nil
}
