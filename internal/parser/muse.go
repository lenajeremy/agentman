package parser

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Meta Muse serves a session's history over its protocol (MSP) as items: the
// user's messages, the agent's, its tool calls, and machinery around them
// (reminders, hooks, compaction notices). These turn the items a person
// would read into Agentman's rows; the machinery is left out, as it is for
// every other agent.

// MuseItem is one MSP transcript item, with the fields the rows are made of.
type MuseItem struct {
	ItemID       string            `json:"itemId"`
	Kind         string            `json:"kind"`
	Status       string            `json:"status"`
	TurnID       string            `json:"turnId"`
	Text         string            `json:"text"`
	DisplayText  string            `json:"displayText"`
	Tool         string            `json:"tool"`
	Args         string            `json:"args"`
	CommandText  string            `json:"commandText"`
	DisplayLabel *MuseDisplayLabel `json:"displayLabel"`
	ExitCode     *int              `json:"exitCode"`
	RecordedAt   string            `json:"recordedAt"`
	Retracted    bool              `json:"retracted"`
	Attachments  []MuseAttachment  `json:"attachments"`
}

// MuseDisplayLabel is how Muse itself names a tool call: while it runs,
// once it is done, and without a tense.
type MuseDisplayLabel struct {
	Active  string `json:"active"`
	Done    string `json:"done"`
	Neutral string `json:"neutral"`
}

// MuseAttachment is a file the user attached to a message.
type MuseAttachment struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// MuseMessages turns a session's items into rows, oldest first.
func MuseMessages(sessionID string, items []MuseItem) []protocol.Message {
	messages := make([]protocol.Message, 0, len(items))
	for _, item := range items {
		if message, ok := MuseMessage(sessionID, item); ok {
			messages = append(messages, message)
		}
	}
	return messages
}

// MuseMessage turns one item into a row, or reports that it is not one a
// person reads.
func MuseMessage(sessionID string, item MuseItem) (protocol.Message, bool) {
	message := protocol.Message{ID: item.ItemID, SessionID: sessionID, Ts: museTime(item.RecordedAt)}
	switch item.Kind {
	case "userMessage":
		if item.Retracted {
			return message, false
		}
		message.Role = protocol.RoleUser
		message.Text = strings.TrimSpace(firstNonEmpty(item.DisplayText, item.Text))
		for _, attachment := range item.Attachments {
			if attachment.Path != "" && (attachment.Kind == "" || attachment.Kind == "image") {
				message.Images = append(message.Images, attachment.Path)
			}
		}
		return message, message.Text != "" || len(message.Images) > 0
	case "agentMessage":
		message.Role = protocol.RoleAssistant
		message.Text = strings.TrimSpace(item.Text)
		return message, message.Text != ""
	case "toolCall", "userShell", "webSearch":
		message.Role = protocol.RoleTool
		message.Tool = museTool(item)
		return message, true
	}
	return message, false
}

// MuseTurnEnd is how a turn ended, as Muse reports it live (turn/completed)
// and in a session's lastTurn.
type MuseTurnEnd struct {
	TurnID   string `json:"turnId"`
	Terminal string `json:"terminal"`
	Reason   string `json:"reason"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// MuseTurnFailed is the row a failed turn leaves. Muse records no item for
// the failure, so a turn its model refused (billing, quota, an outage) would
// otherwise look like Muse silently did nothing.
func MuseTurnFailed(sessionID string, end MuseTurnEnd, ts int64) (protocol.Message, bool) {
	if end.Terminal != "failed" || end.TurnID == "" {
		return protocol.Message{}, false
	}
	reason := end.Reason
	if end.Error != nil && strings.TrimSpace(end.Error.Message) != "" {
		reason = end.Error.Message
	}
	text := "Muse turn failed"
	if reason = strings.TrimSpace(reason); reason != "" {
		text += ": " + reason
	}
	return protocol.Message{
		ID: "turn:" + end.TurnID + ":failed", SessionID: sessionID,
		Role: protocol.RoleSystem, Ts: ts, Text: clip(text, PreviewChars),
	}, true
}

func museTool(item MuseItem) *protocol.Tool {
	tool := &protocol.Tool{Name: firstNonEmpty(item.Tool, item.Kind), Status: museToolStatus(item)}
	if item.Kind == "userShell" {
		tool.Name = "Shell"
	}
	if item.DisplayLabel != nil {
		label := item.DisplayLabel.Done
		if tool.Status == protocol.ToolRunning {
			label = firstNonEmpty(item.DisplayLabel.Active, label)
		}
		tool.Title = strings.TrimSpace(firstNonEmpty(label, item.DisplayLabel.Neutral))
	}
	// What it ran or touched: a command as typed, else the arguments' most
	// telling value.
	tool.Summary = strings.TrimSpace(firstNonEmpty(item.CommandText, museArgSummary(item.Args)))
	return tool
}

func museToolStatus(item MuseItem) protocol.ToolStatus {
	switch item.Status {
	case "inProgress":
		return protocol.ToolRunning
	case "failed", "rejected", "timedOut", "cancelled":
		return protocol.ToolError
	}
	if item.ExitCode != nil && *item.ExitCode != 0 {
		return protocol.ToolError
	}
	return protocol.ToolOK
}

// museArgSummary picks the argument a reader looks for first: a path, a
// command, a pattern, a URL; else nothing, rather than raw JSON.
func museArgSummary(args string) string {
	if strings.TrimSpace(args) == "" {
		return ""
	}
	var fields map[string]any
	if json.Unmarshal([]byte(args), &fields) != nil {
		return ""
	}
	for _, key := range []string{"path", "file_path", "filePath", "command", "cmd", "pattern", "query", "url"} {
		if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func museTime(at string) int64 {
	if at == "" {
		return 0
	}
	if when, err := time.Parse(time.RFC3339Nano, at); err == nil {
		return when.UnixMilli()
	}
	return 0
}
