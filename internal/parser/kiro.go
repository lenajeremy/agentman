package parser

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Kiro CLI writes each session to ~/.kiro/sessions/cli/<id>.jsonl, one event
// per line:
//
//	{"version":"v1","kind":"Prompt","data":{"message_id":…,"content":[…],"meta":{"timestamp":…}}}
//	{"version":"v1","kind":"AssistantMessage","data":{"message_id":…,"content":[…]}}
//	{"version":"v1","kind":"ToolResults","data":{"message_id":…,"content":[…]}}
//
// An assistant message carries its tool calls as toolUse blocks, and their
// results arrive later in a ToolResults event keyed by the same toolUseId.
//
// Only prompts carry a timestamp. That matters more than it looks: the app
// orders a feed by ts, so a reply with no time of its own would sort above the
// prompt that caused it. Message ids are random UUIDs, so they cannot break
// the tie either. The parser therefore stamps every message after a prompt
// with that prompt's time plus a step, which is also why it has to see a
// transcript forwards and from its start.
type KiroParser struct {
	sessionID string
	// turnTs is the latest prompt's time in milliseconds, and step counts the
	// messages emitted since, so each one sorts after the last.
	turnTs int64
	step   int64
	calls  *boundedMap[kiroCall]
}

// kiroCall is a tool call remembered until its result arrives. The settled row
// replaces the running one by id, so it keeps the call's timestamp: taking a
// new one would make the row jump down the feed when the command finished.
type kiroCall struct {
	name    string
	summary string
	ts      int64
}

// NewKiroParser creates a parser bound to one session. Feed it lines in file
// order from the start of the transcript.
func NewKiroParser(sessionID string) *KiroParser {
	return &KiroParser{sessionID: sessionID, calls: newBoundedMap[kiroCall](2000)}
}

type kiroEvent struct {
	Kind string `json:"kind"`
	Data struct {
		MessageID string      `json:"message_id"`
		Content   []kiroBlock `json:"content"`
		Meta      *struct {
			Timestamp int64 `json:"timestamp"`
		} `json:"meta"`
	} `json:"data"`
}

type kiroBlock struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

type kiroToolUse struct {
	ToolUseID string          `json:"toolUseId"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

type kiroToolResult struct {
	ToolUseID string      `json:"toolUseId"`
	Content   []kiroBlock `json:"content"`
	Status    string      `json:"status"`
}

// next returns a timestamp that sorts after everything emitted so far.
func (p *KiroParser) next() int64 {
	p.step++
	return p.turnTs + p.step
}

// Parse implements Parser.
func (p *KiroParser) Parse(line string, offset int64) []protocol.Message {
	var event kiroEvent
	if !decode(line, &event) {
		return nil
	}
	id := event.Data.MessageID
	if id == "" {
		id = fmt.Sprintf("o%d", offset)
	}

	switch event.Kind {
	case "Prompt":
		ts := int64(0)
		if event.Data.Meta != nil {
			ts = event.Data.Meta.Timestamp * 1000
		}
		// Prompts are stamped to the second, so a quick follow-up can share its
		// predecessor's second — or, if the clock stepped, precede it. Never let
		// a new turn sort above the replies to the last one.
		if floor := p.turnTs + p.step + 1; ts < floor {
			ts = floor
		}
		p.turnTs, p.step = ts, 0
		text := kiroText(event.Data.Content)
		if text == "" {
			return nil
		}
		return []protocol.Message{{
			ID: id, SessionID: p.sessionID, Role: protocol.RoleUser, Ts: ts, Text: text,
		}}

	case "AssistantMessage":
		var out []protocol.Message
		for i, block := range event.Data.Content {
			switch block.Kind {
			case "text":
				var text string
				if json.Unmarshal(block.Data, &text) != nil {
					continue
				}
				if text = strings.TrimSpace(text); text == "" {
					continue
				}
				out = append(out, protocol.Message{
					ID: fmt.Sprintf("%s:%d", id, i), SessionID: p.sessionID,
					Role: protocol.RoleAssistant, Ts: p.next(), Text: text,
				})
			case "toolUse":
				var use kiroToolUse
				if json.Unmarshal(block.Data, &use) != nil {
					continue
				}
				callID := use.ToolUseID
				if callID == "" {
					callID = fmt.Sprintf("%s:%d", id, i)
				}
				call := kiroCall{
					name:    kiroToolName(use.Name),
					summary: summarizeKiroInput(use.Name, use.Input),
					ts:      p.next(),
				}
				p.calls.set(callID, call)
				out = append(out, protocol.Message{
					ID: callID, SessionID: p.sessionID, Role: protocol.RoleTool, Ts: call.ts,
					Tool: &protocol.Tool{Name: call.name, Summary: call.summary, Status: protocol.ToolRunning},
				})
			}
			// Thinking arrives redacted — an opaque byte array — so there is
			// nothing in it to show.
		}
		return out

	case "ToolResults":
		var out []protocol.Message
		for _, block := range event.Data.Content {
			if block.Kind != "toolResult" {
				continue
			}
			var result kiroToolResult
			if json.Unmarshal(block.Data, &result) != nil || result.ToolUseID == "" {
				continue
			}
			text, failed := kiroResultText(result.Content)
			status := protocol.ToolOK
			if failed || (result.Status != "" && !strings.EqualFold(result.Status, "success")) {
				status = protocol.ToolError
			}
			call, known := p.calls.get(result.ToolUseID)
			if !known {
				// The call scrolled out of the bounded map. A result with no
				// name to show is still better than a row that stays running.
				call = kiroCall{name: "Tool", ts: p.next()}
			}
			out = append(out, protocol.Message{
				ID: result.ToolUseID, SessionID: p.sessionID, Role: protocol.RoleTool, Ts: call.ts,
				Text: ClipBlock(text, PreviewLines, PreviewChars),
				Tool: &protocol.Tool{Name: call.name, Summary: call.summary, Status: status},
			})
		}
		return out
	}
	return nil
}

// kiroText joins the text blocks of a prompt.
func kiroText(blocks []kiroBlock) string {
	var parts []string
	for _, block := range blocks {
		if block.Kind != "text" {
			continue
		}
		var text string
		if json.Unmarshal(block.Data, &text) == nil && strings.TrimSpace(text) != "" {
			parts = append(parts, strings.TrimSpace(text))
		}
	}
	return strings.Join(parts, "\n\n")
}

// kiroResultText flattens a tool result for the preview, and reports whether a
// command exited unsuccessfully. Kiro marks the call itself a success when the
// command ran at all, so a failing command is only visible in its exit status.
func kiroResultText(blocks []kiroBlock) (string, bool) {
	var parts []string
	failed := false
	for _, block := range blocks {
		switch block.Kind {
		case "text":
			var text string
			if json.Unmarshal(block.Data, &text) == nil {
				parts = append(parts, text)
			}
		case "json":
			var output struct {
				ExitStatus string `json:"exit_status"`
				Stdout     string `json:"stdout"`
				Stderr     string `json:"stderr"`
			}
			if json.Unmarshal(block.Data, &output) == nil &&
				(output.ExitStatus != "" || output.Stdout != "" || output.Stderr != "") {
				if output.ExitStatus != "" && !strings.HasSuffix(output.ExitStatus, " 0") {
					failed = true
				}
				for _, stream := range []string{output.Stdout, output.Stderr} {
					if strings.TrimSpace(stream) != "" {
						parts = append(parts, strings.TrimRight(stream, "\n"))
					}
				}
				continue
			}
			// Some other tool's structured result: show it rather than drop it.
			parts = append(parts, string(block.Data))
		}
	}
	return strings.Join(parts, "\n"), failed
}

// kiroToolName gives Kiro's built-in tools the names the app already knows how
// to render: a "Shell" row shows its command, a "Read" row can open an image.
func kiroToolName(name string) string {
	switch name {
	case "shell", "execute_bash", "execute_cmd":
		return "Shell"
	case "fs_read", "read":
		return "Read"
	case "fs_write", "write":
		return "Write"
	case "grep":
		return "Grep"
	case "glob":
		return "Glob"
	case "web_search":
		return "Web search"
	case "web_fetch":
		return "Web fetch"
	case "":
		return "Tool"
	}
	return name
}

// summarizeKiroInput picks the part of a call worth a glance: the command, the
// path, or the pattern.
//
// The key order is tool-aware on purpose. fs_write carries a "command" field
// too — the edit kind, "create" or "str_replace" — so a generic "command first"
// rule labels every file write with the word "create".
func summarizeKiroInput(name string, raw json.RawMessage) string {
	var input map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &input) != nil {
		return ""
	}
	pick := func(from map[string]any, keys ...string) string {
		for _, key := range keys {
			if value, ok := from[key].(string); ok && strings.TrimSpace(value) != "" {
				return value
			}
		}
		return ""
	}
	if kiroToolName(name) == "Shell" {
		return ClipBlock(pick(input, "command"), CommandLines, CommandChars)
	}
	if path := pick(input, "path", "file_path", "filePath"); path != "" {
		return clip(path, SummaryChars)
	}
	// fs_read batches its reads as a list of operations.
	if operations, ok := input["operations"].([]any); ok && len(operations) > 0 {
		if first, ok := operations[0].(map[string]any); ok {
			if path := pick(first, "path", "file_path"); path != "" {
				return clip(path, SummaryChars)
			}
		}
	}
	if value := pick(input, "pattern", "query", "url", "command"); value != "" {
		return clip(value, SummaryChars)
	}
	return clip(pick(input, "__tool_use_purpose"), SummaryChars)
}
