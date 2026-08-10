package parser

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Codex writes ~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl.
//
// The file interleaves two parallel streams: "response_item" (the raw history
// sent to the model) and "event_msg" (the semantic stream the TUI renders).
// They overlap almost exactly — on a sample session, 175 AgentMessage events
// against 175 assistant response items — so reading both would duplicate every
// message.
//
// We read event_msg/item_completed, the better of the two: it excludes the
// "developer" role (injected instructions the user never wrote), and reports
// commands already parsed and file changes already resolved to paths, rather
// than as raw tool-call payloads.
type CodexParser struct {
	sessionID string
}

// NewCodexParser creates a parser bound to one session.
func NewCodexParser(sessionID string) *CodexParser {
	return &CodexParser{sessionID: sessionID}
}

type codexRecord struct {
	Timestamp string        `json:"timestamp"`
	Type      string        `json:"type"`
	Payload   *codexPayload `json:"payload"`
}

type codexPayload struct {
	Type string     `json:"type"`
	Item *codexItem `json:"item"`
}

type codexItem struct {
	Type    string         `json:"type"`
	ID      string         `json:"id"`
	Content []codexContent `json:"content"`
	Status  string         `json:"status"`

	// CommandExecution
	Command          []string         `json:"command"`
	ParsedCmd        []codexParsedCmd `json:"parsed_cmd"`
	ExitCode         *int             `json:"exit_code"`
	AggregatedOutput string           `json:"aggregated_output"`
	Stdout           string           `json:"stdout"`

	// FileChange
	Changes map[string]json.RawMessage `json:"changes"`

	// McpToolCall
	Server string `json:"server"`
	Tool   string `json:"tool"`
}

type codexContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexParsedCmd struct {
	Type string `json:"type"`
	Cmd  string `json:"cmd"`
}

// Parse implements Parser.
func (p *CodexParser) Parse(line string, offset int64) []protocol.Message {
	var rec codexRecord
	if !decode(line, &rec) {
		return nil
	}
	if rec.Type != "event_msg" || rec.Payload == nil {
		return nil
	}
	if rec.Payload.Type != "item_completed" || rec.Payload.Item == nil {
		return nil
	}

	item := rec.Payload.Item
	ts := parseTime(rec.Timestamp)
	id := item.ID
	if id == "" {
		id = fmt.Sprintf("o%d", offset)
	}
	base := protocol.Message{ID: id, SessionID: p.sessionID, Ts: ts}

	switch item.Type {
	case "UserMessage":
		body := collectCodexText(item.Content)
		if body == "" {
			return nil
		}
		base.Role = protocol.RoleUser
		base.Text = body
		return []protocol.Message{base}

	case "AgentMessage":
		body := collectCodexText(item.Content)
		if body == "" {
			return nil
		}
		base.Role = protocol.RoleAssistant
		base.Text = body
		return []protocol.Message{base}

	case "CommandExecution":
		// parsed_cmd is Codex's own readable rendering; command is raw argv,
		// which is usually just a shell wrapper.
		command := ""
		if len(item.ParsedCmd) > 0 {
			command = item.ParsedCmd[0].Cmd
		}
		if command == "" && len(item.Command) > 0 {
			command = item.Command[len(item.Command)-1]
		}

		status := protocol.ToolOK
		switch {
		case item.Status == "in_progress":
			status = protocol.ToolRunning
		case item.Status == "failed", item.ExitCode != nil && *item.ExitCode != 0:
			status = protocol.ToolError
		}

		output := item.AggregatedOutput
		if output == "" {
			output = item.Stdout
		}

		base.Role = protocol.RoleTool
		base.Text = clip(output, PreviewChars)
		base.Tool = &protocol.Tool{
			Name:    "Shell",
			Summary: clip(command, SummaryChars),
			Status:  status,
		}
		return []protocol.Message{base}

	case "FileChange":
		paths := make([]string, 0, len(item.Changes))
		for path := range item.Changes {
			paths = append(paths, path)
		}
		// Map iteration is random in Go; sort so the summary is stable across
		// re-reads and a message keeps the same content when paged again.
		sort.Strings(paths)

		summary := ""
		switch len(paths) {
		case 0:
		case 1:
			summary = paths[0]
		default:
			shown := paths
			if len(shown) > 3 {
				shown = shown[:3]
			}
			names := make([]string, len(shown))
			for i, path := range shown {
				names[i] = baseName(path)
			}
			summary = fmt.Sprintf("%d files: %s", len(paths), strings.Join(names, ", "))
		}

		base.Role = protocol.RoleTool
		base.Tool = &protocol.Tool{Name: "Edit", Summary: clip(summary, SummaryChars), Status: protocol.ToolOK}
		return []protocol.Message{base}

	case "McpToolCall":
		server, tool := item.Server, item.Tool
		if server == "" {
			server = "mcp"
		}
		if tool == "" {
			tool = "call"
		}
		status := protocol.ToolOK
		if item.Status == "failed" {
			status = protocol.ToolError
		}
		base.Role = protocol.RoleTool
		base.Tool = &protocol.Tool{Name: server + "." + tool, Status: status}
		return []protocol.Message{base}

	case "ContextCompaction":
		base.Role = protocol.RoleSystem
		base.Text = "Context compacted"
		return []protocol.Message{base}

	default:
		// "Reasoning" is dropped for the same reason as Claude's "thinking"
		// blocks: long, largely opaque, and not what a phone glance is for.
		return nil
	}
}

// CodexStateFromLine reads turn-level transitions from the same event stream.
// Used to drive the busy/idle dot when hooks are not installed.
func CodexStateFromLine(line string) (protocol.State, bool) {
	var rec codexRecord
	if !decode(line, &rec) || rec.Type != "event_msg" || rec.Payload == nil {
		return "", false
	}
	switch rec.Payload.Type {
	case "task_started":
		return protocol.StateBusy, true
	case "task_complete", "turn_aborted":
		return protocol.StateIdle, true
	default:
		return "", false
	}
}

// collectCodexText joins text blocks. Events use "text" and items use "Text",
// so matching is case-insensitive.
func collectCodexText(content []codexContent) string {
	var parts []string
	for _, block := range content {
		switch strings.ToLower(block.Type) {
		case "text", "input_text", "output_text":
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}
