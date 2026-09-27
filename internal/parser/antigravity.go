package parser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Antigravity CLI records a conversation as a trajectory of steps, and writes
// them to brain/<id>/.system_generated/logs/transcript_full.jsonl:
//
//	{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":…,"content":"<USER_REQUEST>…"}
//	{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE",…,"tool_calls":[{"name":"run_command","args":{…}}]}
//	{"step_index":2,"source":"MODEL","type":"GENERIC",…,"content":"…The command exited with code 0.\nOutput:…"}
//	{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE",…,"content":"There is 1 file…"}
//
// transcript.jsonl beside it is the same trajectory with tool arguments
// double-encoded as strings and long results truncated, so it is not used.
//
// Tool calls carry no id, and a result is simply the step after its call. So
// a call is keyed by the index of the step that will hold its result: call j
// of step P is answered at step P+1+j. Reading forwards or backwards, the call
// and its result arrive at the same id without either having to be remembered.
// Ids are zero-padded because the app breaks timestamp ties by id, and steps
// share a second often enough that "s10" sorting before "s9" would show.
type AntigravityParser struct {
	sessionID string
	calls     *boundedMap[antigravityCall]
	outcomes  *boundedMap[toolOutcome]
}

type antigravityCall struct {
	name    string
	summary string
	ts      int64
}

// NewAntigravityParser creates a parser bound to one session. It reads a
// transcript in either direction.
func NewAntigravityParser(sessionID string) *AntigravityParser {
	return &AntigravityParser{
		sessionID: sessionID,
		calls:     newBoundedMap[antigravityCall](2000),
		outcomes:  newBoundedMap[toolOutcome](2000),
	}
}

type antigravityStep struct {
	StepIndex int    `json:"step_index"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	Content   string `json:"content"`
	ToolCalls []struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"tool_calls"`
}

var (
	antigravityRequest = regexp.MustCompile(`(?s)<USER_REQUEST>\s*(.*?)\s*</USER_REQUEST>`)
	antigravityExit    = regexp.MustCompile(`The command exited with code (-?\d+)`)
	// antigravityStamps are the bookkeeping lines a tool result opens with.
	antigravityStamps = regexp.MustCompile(`^(?:Created At|Completed At): `)
)

func antigravityID(step int) string { return fmt.Sprintf("s%06d", step) }

// Parse implements Parser.
func (p *AntigravityParser) Parse(line string, offset int64) []protocol.Message {
	var step antigravityStep
	if !decode(line, &step) {
		return nil
	}
	ts := parseTime(step.CreatedAt)

	switch step.Type {
	case "USER_INPUT":
		text := AntigravityRequest(step.Content)
		if text == "" {
			return nil
		}
		return []protocol.Message{{
			ID: antigravityID(step.StepIndex), SessionID: p.sessionID,
			Role: protocol.RoleUser, Ts: ts, Text: text,
		}}

	case "PLANNER_RESPONSE":
		var out []protocol.Message
		if text := strings.TrimSpace(step.Content); text != "" {
			out = append(out, protocol.Message{
				ID: antigravityID(step.StepIndex), SessionID: p.sessionID,
				Role: protocol.RoleAssistant, Ts: ts, Text: text,
			})
		}
		for j, call := range step.ToolCalls {
			id := antigravityID(step.StepIndex + 1 + j)
			remembered := antigravityCall{
				name:    antigravityToolName(call.Name),
				summary: summarizeAntigravityArgs(call.Name, call.Args),
				ts:      ts,
			}
			p.calls.set(id, remembered)
			message := protocol.Message{
				ID: id, SessionID: p.sessionID, Role: protocol.RoleTool, Ts: ts,
				Tool: &protocol.Tool{Name: remembered.name, Summary: remembered.summary, Status: protocol.ToolRunning},
			}
			// Paging backwards meets the result first.
			if outcome, ok := p.outcomes.get(id); ok {
				message.Tool.Status = outcome.status
				message.Text = outcome.preview
			}
			out = append(out, message)
		}
		return out

	case "GENERIC":
		id := antigravityID(step.StepIndex)
		outcome := antigravityOutcome(step.Content, step.Status)
		p.outcomes.set(id, outcome)
		call, known := p.calls.get(id)
		if !known {
			// Paging backwards, the call is still ahead; it picks this up. A
			// generic step that answers no call is not something to show.
			return nil
		}
		return []protocol.Message{{
			ID: id, SessionID: p.sessionID, Role: protocol.RoleTool, Ts: call.ts,
			Text: outcome.preview,
			Tool: &protocol.Tool{Name: call.name, Summary: call.summary, Status: outcome.status},
		}}
	}
	return nil
}

// AntigravityRequest extracts what the user typed from a USER_INPUT step.
// Antigravity wraps it with context of its own — the local time, settings
// changes — none of which the user wrote.
func AntigravityRequest(content string) string {
	if match := antigravityRequest.FindStringSubmatch(content); match != nil {
		return strings.TrimSpace(match[1])
	}
	if strings.Contains(content, "<") {
		return "" // entirely machine context, no request in it
	}
	return strings.TrimSpace(content)
}

func antigravityOutcome(content, status string) toolOutcome {
	outcome := toolOutcome{status: protocol.ToolOK}
	if match := antigravityExit.FindStringSubmatch(content); match != nil {
		if code, err := strconv.Atoi(match[1]); err == nil && code != 0 {
			outcome.status = protocol.ToolError
		}
	}
	switch strings.ToUpper(status) {
	case "ERROR", "FAILED", "CANCELED", "CANCELLED":
		outcome.status = protocol.ToolError
	}
	var kept []string
	for _, line := range strings.Split(content, "\n") {
		if !antigravityStamps.MatchString(line) {
			kept = append(kept, line)
		}
	}
	outcome.preview = ClipBlock(strings.TrimSpace(strings.Join(kept, "\n")), PreviewLines, PreviewChars)
	return outcome
}

// antigravityToolName gives Antigravity's tools the names the app knows how
// to render: a "Shell" row shows its command, a "Read" row can open an image.
func antigravityToolName(name string) string {
	switch name {
	case "run_command":
		return "Shell"
	case "view_file", "view_file_outline", "view_code_item":
		return "Read"
	case "write_to_file":
		return "Write"
	case "replace_file_content", "multi_replace_file_content":
		return "Edit"
	case "list_dir":
		return "List"
	case "grep_search":
		return "Grep"
	case "find_by_name":
		return "Find"
	case "search_web":
		return "Web search"
	case "read_url_content":
		return "Web fetch"
	case "":
		return "Tool"
	}
	return name
}

// summarizeAntigravityArgs picks the part of a call worth a glance. Argument
// names are Antigravity's own — CommandLine, AbsolutePath, TargetFile.
func summarizeAntigravityArgs(name string, raw json.RawMessage) string {
	var args map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &args) != nil {
		return ""
	}
	pick := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
				return value
			}
		}
		return ""
	}
	if name == "run_command" {
		return ClipBlock(pick("CommandLine"), CommandLines, CommandChars)
	}
	if value := pick("AbsolutePath", "TargetFile", "File", "DirectoryPath", "SearchPath",
		"Query", "Url", "SearchDirectory"); value != "" {
		return clip(value, SummaryChars)
	}
	return clip(pick("toolSummary", "toolAction"), SummaryChars)
}
