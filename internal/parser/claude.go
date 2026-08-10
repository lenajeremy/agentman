package parser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Claude Code writes ~/.claude/projects/<cwd-slug>/<sessionId>.jsonl.
//
// Only "user" and "assistant" records carry conversation. The rest of the file
// is bookkeeping the UI has no use for — mode changes, generated titles,
// file-history snapshots — and dropping it early is most of what makes the
// mobile feed readable.
var claudeIgnoredTypes = map[string]bool{
	"mode":                  true,
	"permission-mode":       true,
	"ai-title":              true,
	"attachment":            true,
	"file-history-snapshot": true,
	"file-history-delta":    true,
	"last-prompt":           true,
	"queue-operation":       true,
	"system":                true,
	"summary":               true,
}

// IsKnownClaudeRecordType reports whether a transcript record type is one this
// parser recognizes — either conversation or known bookkeeping.
//
// Claude Code's transcript format is not a published API. `am doctor` scans a
// recent transcript through this so an unrecognized record type after a CLI
// upgrade surfaces as an explicit warning, rather than as messages silently
// vanishing from the feed.
func IsKnownClaudeRecordType(recordType string) bool {
	return recordType == "user" || recordType == "assistant" || claudeIgnoredTypes[recordType]
}

// Slash-command scaffolding the CLI injects as though the user had typed it.
var claudeCommandWrapper = regexp.MustCompile(
	`^\s*<(command-name|command-message|command-args|local-command-stdout|user-prompt-submit-hook)`)

// claudeRecord is the subset of a transcript line we rely on.
type claudeRecord struct {
	Type        string         `json:"type"`
	UUID        string         `json:"uuid"`
	Timestamp   string         `json:"timestamp"`
	IsMeta      bool           `json:"isMeta"`
	IsSidechain bool           `json:"isSidechain"`
	Message     *claudeMessage `json:"message"`
}

type claudeMessage struct {
	Role string `json:"role"`
	// Content is a bare string for a typed prompt, or an array of blocks.
	Content json.RawMessage `json:"content"`
}

type claudeBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text"`
	// tool_use
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	// tool_result
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// ClaudeParser normalizes a Claude Code transcript.
type ClaudeParser struct {
	sessionID string
	// outcomes holds results seen before their call — the backward-paging case.
	outcomes *boundedMap[toolOutcome]
	// toolNames holds names seen before their result — the live-tail case.
	toolNames *boundedMap[string]
}

// NewClaudeParser creates a parser bound to one session.
func NewClaudeParser(sessionID string) *ClaudeParser {
	return &ClaudeParser{
		sessionID: sessionID,
		outcomes:  newBoundedMap[toolOutcome](2000),
		toolNames: newBoundedMap[string](2000),
	}
}

// Parse implements Parser.
func (p *ClaudeParser) Parse(line string, offset int64) []protocol.Message {
	var rec claudeRecord
	if !decode(line, &rec) {
		return nil
	}
	if rec.Type != "user" && rec.Type != "assistant" {
		return nil
	}
	if rec.IsMeta || rec.Message == nil {
		return nil
	}

	uuid := rec.UUID
	if uuid == "" {
		uuid = fmt.Sprintf("o%d", offset)
	}
	ts := parseTime(rec.Timestamp)
	role := protocol.Role(rec.Type)

	// A bare string is a straight typed prompt.
	var plain string
	if json.Unmarshal(rec.Message.Content, &plain) == nil {
		body := strings.TrimSpace(plain)
		if body == "" || claudeCommandWrapper.MatchString(body) {
			return nil
		}
		return []protocol.Message{{
			ID: uuid, SessionID: p.sessionID, Role: role, Ts: ts,
			Text: body, IsSidechain: rec.IsSidechain,
		}}
	}

	var blocks []claudeBlock
	if json.Unmarshal(rec.Message.Content, &blocks) != nil {
		return nil
	}

	var out []protocol.Message
	for i, block := range blocks {
		switch block.Type {
		case "text":
			body := strings.TrimSpace(block.Text)
			if body == "" || claudeCommandWrapper.MatchString(body) {
				continue
			}
			out = append(out, protocol.Message{
				ID: fmt.Sprintf("%s:%d", uuid, i), SessionID: p.sessionID,
				Role: role, Ts: ts, Text: body, IsSidechain: rec.IsSidechain,
			})

		case "tool_use":
			id := block.ID
			if id == "" {
				id = fmt.Sprintf("%s:%d", uuid, i)
			}
			name := block.Name
			if name == "" {
				name = "tool"
			}
			p.toolNames.set(id, name)

			msg := protocol.Message{
				ID: id, SessionID: p.sessionID, Role: protocol.RoleTool, Ts: ts,
				Tool: &protocol.Tool{
					Name:    name,
					Summary: summarizeToolInput(name, block.Input),
					Status:  protocol.ToolRunning,
				},
				IsSidechain: rec.IsSidechain,
			}
			// A known outcome means we are paging backwards and met the result
			// first, so this row can be emitted complete in one go.
			if outcome, ok := p.outcomes.get(id); ok {
				msg.Tool.Status = outcome.status
				msg.Text = outcome.preview
			}
			out = append(out, msg)

		case "tool_result":
			if block.ToolUseID == "" {
				continue
			}
			status := protocol.ToolOK
			if block.IsError {
				status = protocol.ToolError
			}
			outcome := toolOutcome{status: status, preview: clip(flattenClaudeResult(block.Content), PreviewChars)}
			p.outcomes.set(block.ToolUseID, outcome)

			// Reading forwards the row already exists, so re-emit it under the
			// same ID to settle it. The app upserts by ID, so this replaces the
			// "running" row rather than adding a second one.
			if name, ok := p.toolNames.get(block.ToolUseID); ok {
				out = append(out, protocol.Message{
					ID: block.ToolUseID, SessionID: p.sessionID,
					Role: protocol.RoleTool, Ts: ts, Text: outcome.preview,
					Tool:        &protocol.Tool{Name: name, Status: status},
					IsSidechain: rec.IsSidechain,
				})
			}

			// "thinking" blocks are deliberately dropped: long, mostly opaque
			// signature payload, and not what a glance at a phone is for.
		}
	}
	return out
}

// flattenClaudeResult handles a tool result arriving as a string or as an
// array of content blocks.
func flattenClaudeResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return plain
	}
	var blocks []claudeBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch {
		case b.Text != "":
			parts = append(parts, b.Text)
		case b.Type == "image":
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "\n")
}

// summarizeToolInput reduces a tool invocation to the one detail a human
// scanning a feed wants: the command, the path, the pattern. Falls back to
// compact JSON so an unknown tool still shows something useful.
func summarizeToolInput(name string, raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var input map[string]any
	if json.Unmarshal(raw, &input) != nil {
		return ""
	}

	pick := func(keys ...string) string {
		for _, key := range keys {
			if v, ok := input[key].(string); ok && strings.TrimSpace(v) != "" {
				return v
			}
		}
		return ""
	}

	switch name {
	case "Bash", "BashOutput":
		return clip(pick("command", "description"), SummaryChars)
	case "Read", "Write", "NotebookEdit":
		return pick("file_path", "notebook_path")
	case "Edit":
		return pick("file_path")
	case "Glob", "Grep":
		return pick("pattern")
	case "WebFetch":
		return pick("url")
	case "WebSearch":
		return pick("query")
	case "Task", "Agent":
		return clip(pick("description", "prompt"), SummaryChars)
	case "TodoWrite":
		return ""
	default:
		if direct := pick("command", "file_path", "path", "query", "pattern", "url"); direct != "" {
			return clip(direct, SummaryChars)
		}
		compact, err := json.Marshal(input)
		if err != nil {
			return ""
		}
		return clip(string(compact), SummaryChars)
	}
}

func parseTime(value string) int64 {
	if value == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}
