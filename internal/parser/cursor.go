package parser

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Cursor writes ~/.cursor/projects/<project>/agent-transcripts/<uuid>/<uuid>.jsonl.
//
// Each line is one record in one of two shapes:
//
//	{"role": "user"|"assistant", "message": {"content": [{"type": "text"|"tool_use", ...}]}}
//	{"type": "turn_ended", "status": "success"|"error", "error": "..."}
//
// Tool results are not recorded as separate lines: an assistant line carries
// its tool_use parts, and the next assistant line continues after the tools
// ran. There are no per-record timestamps or model identifiers anywhere in
// the file — recency comes from the file's mtime, and the model stays empty.
type CursorParser struct {
	sessionID string
}

// NewCursorParser creates a parser bound to one session.
func NewCursorParser(sessionID string) *CursorParser {
	return &CursorParser{sessionID: sessionID}
}

type cursorRecord struct {
	Role    string         `json:"role"`
	Type    string         `json:"type"`
	Status  string         `json:"status"`
	Error   string         `json:"error"`
	Message *cursorMessage `json:"message"`
}

type cursorMessage struct {
	Content []cursorContent `json:"content"`
}

type cursorContent struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// Parse implements Parser.
func (p *CursorParser) Parse(line string, offset int64) []protocol.Message {
	var rec cursorRecord
	if !decode(line, &rec) {
		return nil
	}

	// Turn boundaries carry no display content on success. A failed turn
	// would otherwise look like the agent silently did nothing.
	if rec.Role == "" {
		if rec.Type != "turn_ended" || rec.Status == "success" {
			return nil
		}
		text := "Cursor turn failed"
		if rec.Error != "" {
			text += ": " + rec.Error
		}
		return []protocol.Message{{
			ID: fmt.Sprintf("o%d:error", offset), SessionID: p.sessionID,
			Role: protocol.RoleSystem, Text: clip(text, PreviewChars),
		}}
	}

	if rec.Message == nil {
		return nil
	}

	var out []protocol.Message
	var textParts []string
	for i, part := range rec.Message.Content {
		switch part.Type {
		case "text":
			if part.Text != "" {
				textParts = append(textParts, part.Text)
			}
		case "tool_use":
			// One message per invocation, namespaced by part index: Cursor
			// numbers nothing, so the offset alone would collapse every tool
			// in one assistant line into a single row downstream.
			name := part.Name
			if name == "" {
				name = "tool"
			}
			summary := clip(cursorToolSummary(name, part.Input), SummaryChars)
			if summary == name {
				// No readable detail beyond the name itself; leave it
				// unset so the UI does not print the name twice.
				summary = ""
			}
			out = append(out, protocol.Message{
				ID: fmt.Sprintf("o%d:tool%d", offset, i), SessionID: p.sessionID,
				Role: protocol.RoleTool,
				Tool: &protocol.Tool{
					Name: name,
					// Cursor records the call but neither its result nor its
					// completion. Leave status unset rather than claiming
					// success or showing an endless running indicator.
					Summary: summary,
				},
			})
		}
	}

	if body := cleanCursorText(strings.Join(textParts, "\n")); body != "" {
		role := protocol.RoleAssistant
		if rec.Role == "user" {
			role = protocol.RoleUser
			body = cleanCursorUserText(body)
			if body == "" {
				// A user record with only scaffolding (e.g. a bare
				// timestamp) is not worth a row.
				return out
			}
		}
		out = append([]protocol.Message{{
			ID: fmt.Sprintf("o%d", offset), SessionID: p.sessionID,
			Role: role, Text: clip(body, PreviewChars),
		}}, out...)
	}
	return out
}

// CursorSessionName labels a session from its opening user query, clipped to
// a phone-readable length. Only the first record is read: transcripts are
// append-only, so the opening query never changes. It returns "" when the
// file cannot be read or opens with anything but a displayable user record,
// and callers fall back to the working directory base.
func CursorSessionName(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// The opening record is a short user prompt; still, bound the read so a
	// corrupt prefix cannot allocate without limit.
	const maxFirstLine = 64 * 1024
	scanner.Buffer(make([]byte, maxFirstLine), maxFirstLine)
	if !scanner.Scan() {
		return ""
	}
	var rec cursorRecord
	if !decode(scanner.Text(), &rec) || rec.Role != "user" || rec.Message == nil {
		return ""
	}
	var parts []string
	for _, part := range rec.Message.Content {
		if part.Type == "text" && part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	if body := cleanCursorUserText(strings.Join(parts, "\n")); body != "" {
		return clip(body, 48)
	}
	return ""
}

// CursorStateFromLine reads turn-level transitions from the same record
// stream. Used to drive the busy/idle dot when hooks are not installed: the
// latest complete line decides, so anything unrecognized is skipped rather
// than reported, letting an older decisive line further back win.
func CursorStateFromLine(line string) (protocol.State, bool) {
	var rec cursorRecord
	if !decode(line, &rec) {
		return "", false
	}
	if rec.Role == "" {
		if rec.Type != "turn_ended" {
			return "", false
		}
		return protocol.StateIdle, true
	}
	if rec.Role == "user" || rec.Role == "assistant" {
		return protocol.StateBusy, true
	}
	return "", false
}

// cleanCursorUserText unwraps the envelope Cursor puts around prompts:
// "<timestamp>...</timestamp>\n<user_query>\n...\n</user_query>".
// Without tags the text is used as-is.
func cleanCursorUserText(text string) string {
	inner := extractTag(text, "user_query")
	if inner == "" {
		inner = text
	}
	// A timestamp without a query is scaffolding, not a message.
	inner = removeTag(inner, "timestamp")
	return strings.TrimSpace(inner)
}

// cleanCursorText trims assistant text. Assistant records carry no envelope,
// so this is only whitespace.
func cleanCursorText(text string) string {
	return strings.TrimSpace(text)
}

// extractTag returns the trimmed contents of the first <name>...</name> pair,
// or "" when the pair is absent.
func extractTag(text, name string) string {
	open, close := "<"+name+">", "</"+name+">"
	start := strings.Index(text, open)
	if start < 0 {
		return ""
	}
	rest := text[start+len(open):]
	end := strings.Index(rest, close)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

// removeTag drops every <name>...</name> pair including its contents, and any
// stray opening/closing markers left behind.
func removeTag(text, name string) string {
	open, close := "<"+name+">", "</"+name+">"
	for {
		start := strings.Index(text, open)
		if start < 0 {
			break
		}
		rest := text[start+len(open):]
		end := strings.Index(rest, close)
		if end < 0 {
			text = text[:start]
			break
		}
		text = text[:start] + rest[end+len(close):]
	}
	return text
}

// cursorToolSummary renders one readable line for a tool invocation. The input
// schema varies per tool, so this prefers the keys Cursor's own tools use, in
// decreasing order of readability, and falls back to the tool name.
func cursorToolSummary(name string, input map[string]any) string {
	for _, key := range []string{"description", "command", "pattern", "path", "glob_pattern", "target_directory"} {
		if value, ok := input[key]; ok {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return name
}
