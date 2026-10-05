package parser

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Cursor's ACP server (`agent acp`) reports a tool call as a run of
// session/update notifications: tool_call {toolCallId, title, kind,
// status:"pending", rawInput, locations}, then tool_call_update
// {status:"in_progress"}, then {status:"completed", content, rawOutput}. Each
// update carries only what changed, so a row is built from everything seen
// for the call so far.
//
// The title is Cursor's own one-liner ("`ls -la`", "Read notes.txt (1 - 10)",
// "Edit `notes.txt`"). It used to be the row's name, which gave every shell
// command its own name and no icon, and nothing else was read: no output, no
// diff, and a command that exited non-zero showed as a success, because
// Cursor reports that as "completed" with the exit code in rawOutput.
//
// Rows use the names the terminal transcript uses (Shell, Read, Edit …), so a
// chat reads the same whichever way it was started.

// CursorACPToolCall accumulates the updates for one call.
type CursorACPToolCall struct {
	Title     string          `json:"title"`
	Kind      string          `json:"kind"`
	Status    string          `json:"status"`
	RawInput  json.RawMessage `json:"rawInput"`
	RawOutput json.RawMessage `json:"rawOutput"`
	Content   json.RawMessage `json:"content"`
	Locations []struct {
		Path string `json:"path"`
	} `json:"locations"`
	// Todos and ImagePath arrive outside the update stream, through
	// Cursor's cursor/update_todos and cursor/generate_image requests.
	Todos     string `json:"-"`
	ImagePath string `json:"-"`
}

// Merge folds one tool_call or tool_call_update into the call.
func (c *CursorACPToolCall) Merge(update json.RawMessage) {
	var next CursorACPToolCall
	if json.Unmarshal(update, &next) != nil {
		return
	}
	if next.Title != "" {
		c.Title = next.Title
	}
	if next.Kind != "" {
		c.Kind = next.Kind
	}
	if next.Status != "" {
		c.Status = next.Status
	}
	if len(next.RawInput) > 0 && string(next.RawInput) != "null" && string(next.RawInput) != "{}" {
		c.RawInput = next.RawInput
	}
	if len(next.RawOutput) > 0 && string(next.RawOutput) != "null" {
		c.RawOutput = next.RawOutput
	}
	if len(next.Content) > 0 && string(next.Content) != "null" {
		c.Content = next.Content
	}
	if len(next.Locations) > 0 {
		c.Locations = next.Locations
	}
}

var cursorACPBackticks = regexp.MustCompile("^`(.*)`$")

// Row is the call as a feed row: its tool, and its output once it has one.
func (c CursorACPToolCall) Row() (*protocol.Tool, string) {
	var input map[string]any
	_ = json.Unmarshal(c.RawInput, &input)
	name := cursorACPToolName(c.Kind, c.Title, input)
	tool := &protocol.Tool{Name: name, Summary: cursorACPSummary(name, c, input), Status: protocol.ToolRunning}
	switch c.Status {
	case "completed":
		tool.Status = protocol.ToolOK
	case "failed", "cancelled":
		tool.Status = protocol.ToolError
	default:
		return tool, ""
	}
	status, text := cursorACPOutput(name, c, input)
	if status == protocol.ToolError {
		tool.Status = status
	}
	return tool, text
}

func cursorACPToolName(kind, title string, input map[string]any) string {
	if named, ok := input["_toolName"].(string); ok {
		switch named {
		case "updateTodos":
			return "TodoWrite"
		case "task":
			return "Task"
		case "createPlan":
			return "CreatePlan"
		case "generateImage":
			return "GenerateImage"
		case "askQuestion":
			return "AskQuestion"
		}
	}
	if tool, ok := input["toolName"].(string); ok && tool != "" {
		return tool // an MCP tool, named by its server
	}
	_, hasPattern := input["pattern"]
	_, hasPath := input["path"]
	switch kind {
	case "execute":
		return "Shell"
	case "read":
		if _, ok := input["paths"]; ok {
			return "ReadLints"
		}
		return "Read"
	case "edit":
		return "Edit"
	case "delete":
		return "Delete"
	case "fetch":
		return "WebFetch"
	case "switch_mode":
		return "SwitchMode"
	case "search":
		switch {
		case input["searchTerm"] != nil:
			return "WebSearch"
		case input["query"] != nil:
			return "SemanticSearch"
		case hasPattern && hasPath:
			return "Grep"
		case hasPattern:
			return "Glob"
		case hasPath:
			return "LS"
		}
		return "Search"
	}
	if title = strings.TrimSpace(title); title != "" {
		if word, _, ok := strings.Cut(title, ":"); ok && !strings.Contains(word, " ") {
			return word
		}
		return clip(title, SummaryChars)
	}
	return "tool"
}

func cursorACPSummary(name string, c CursorACPToolCall, input map[string]any) string {
	switch name {
	case "Shell":
		command := cursorCLIPick(input, "command")
		if command == "" {
			// A permission-time card has only the title, "`cmd`".
			if match := cursorACPBackticks.FindStringSubmatch(strings.TrimSpace(c.Title)); match != nil {
				command = match[1]
			}
		}
		return ClipBlock(command, CommandLines, CommandChars)
	case "Read", "Edit", "Delete", "LS":
		if path := cursorCLIPick(input, "path"); path != "" {
			return path
		}
		if len(c.Locations) > 0 {
			return c.Locations[0].Path
		}
	case "Grep", "Glob":
		return clip(cursorCLIPick(input, "pattern"), SummaryChars)
	case "WebFetch":
		return clip(cursorCLIPick(input, "url"), SummaryChars)
	case "WebSearch":
		return clip(cursorCLIPick(input, "searchTerm"), SummaryChars)
	case "SemanticSearch":
		return clip(cursorCLIPick(input, "query"), SummaryChars)
	case "Task":
		return clip(cursorCLIPick(input, "description", "prompt"), SummaryChars)
	case "CreatePlan":
		return clip(cursorCLIPick(input, "name"), SummaryChars)
	case "SwitchMode":
		return clip(cursorCLIPick(input, "targetModeId"), SummaryChars)
	case "GenerateImage":
		if c.ImagePath != "" {
			return c.ImagePath
		}
		return clip(cursorCLIPick(input, "description"), SummaryChars)
	case "ReadLints":
		if paths, ok := input["paths"].([]any); ok && len(paths) > 0 {
			if first, ok := paths[0].(string); ok {
				return first
			}
		}
	case "TodoWrite", "AskQuestion":
		return ""
	}
	if server, ok := input["providerIdentifier"].(string); ok && server != "" {
		args, _ := json.Marshal(input["args"])
		return clip(server+": "+string(args), SummaryChars)
	}
	return clip(strings.Trim(strings.TrimSpace(c.Title), "`"), SummaryChars)
}

func cursorACPOutput(name string, c CursorACPToolCall, input map[string]any) (protocol.ToolStatus, string) {
	if c.Todos != "" {
		return protocol.ToolOK, c.Todos
	}
	if c.ImagePath != "" {
		return protocol.ToolOK, "[image]"
	}
	var output struct {
		ExitCode  *int   `json:"exitCode"`
		Stdout    string `json:"stdout"`
		Stderr    string `json:"stderr"`
		Content   string `json:"content"`
		Error     string `json:"error"`
		Rejected  bool   `json:"rejected"`
		Reason    string `json:"reason"`
		Denied    bool   `json:"permissionDenied"`
		Matches   *int   `json:"totalMatches"`
		Files     *int   `json:"totalFiles"`
		Truncated bool   `json:"truncated"`
	}
	_ = json.Unmarshal(c.RawOutput, &output)
	switch {
	case output.Rejected:
		text := "Rejected"
		if output.Reason != "" {
			text += ": " + output.Reason
		}
		return protocol.ToolError, text
	case output.Denied:
		return protocol.ToolError, "Permission denied"
	case output.Error != "":
		return protocol.ToolError, ClipBlock(output.Error, PreviewLines, PreviewChars)
	}
	if diff := cursorACPDiffs(c.Content); diff != "" {
		return protocol.ToolOK, ClipBlock(diff, PreviewLines, PreviewChars)
	}
	switch name {
	case "Shell":
		text := strings.TrimRight(output.Stdout, "\n")
		if output.Stderr != "" {
			text = strings.TrimSpace(text + "\n" + output.Stderr)
		}
		if output.ExitCode != nil && *output.ExitCode != 0 {
			return protocol.ToolError, ClipBlock(strings.TrimSpace(fmt.Sprintf("Exit code %d\n%s", *output.ExitCode, text)), PreviewLines, PreviewChars)
		}
		return protocol.ToolOK, ClipBlock(text, PreviewLines, PreviewChars)
	case "Read":
		if output.Content != "" {
			return protocol.ToolOK, ClipBlock(output.Content, PreviewLines, PreviewChars)
		}
		if cursorACPImagePath(cursorCLIPick(input, "path")) {
			return protocol.ToolOK, "[image]"
		}
	case "Grep":
		if output.Matches != nil {
			return protocol.ToolOK, fmt.Sprintf("%d matches%s", *output.Matches, truncatedNote(output.Truncated))
		}
	case "Glob":
		if output.Files != nil {
			return protocol.ToolOK, fmt.Sprintf("%d files%s", *output.Files, truncatedNote(output.Truncated))
		}
	case "CreatePlan":
		if plan := cursorCLIPick(input, "plan"); plan != "" {
			return protocol.ToolOK, ClipBlock(plan, PreviewLines, PreviewChars)
		}
	}
	return protocol.ToolOK, ClipBlock(cursorACPContentText(c.Content), PreviewLines, PreviewChars)
}

func truncatedNote(truncated bool) string {
	if truncated {
		return " (truncated)"
	}
	return ""
}

func cursorACPImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

type cursorACPContent struct {
	Type    string  `json:"type"`
	Path    string  `json:"path"`
	OldText *string `json:"oldText"`
	NewText string  `json:"newText"`
	Content struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// cursorACPDiffs renders every {"type":"diff"} block as a unified diff.
func cursorACPDiffs(raw json.RawMessage) string {
	var blocks []cursorACPContent
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var diffs []string
	for _, block := range blocks {
		if block.Type != "diff" {
			continue
		}
		old := ""
		if block.OldText != nil {
			old = *block.OldText
		}
		if diff := UnifiedDiff(block.Path, old, block.NewText); diff != "" {
			diffs = append(diffs, diff)
		}
	}
	return strings.Join(diffs, "\n")
}

func cursorACPContentText(raw json.RawMessage) string {
	var blocks []cursorACPContent
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var texts []string
	for _, block := range blocks {
		if block.Type == "content" && block.Content.Type == "text" && block.Content.Text != "" {
			texts = append(texts, block.Content.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// CursorACPPermission describes a session/request_permission for the phone.
//
// Cursor sends the action as a tool card without rawInput: a shell command is
// its title ("`rm -rf build`") plus the reason as text content, a write is a
// diff, an MCP call is its arguments as JSON. The detail was read from
// rawInput and so was always empty — an approval with nothing to approve.
func CursorACPPermission(toolCall json.RawMessage) (title, prompt, detail string) {
	var card CursorACPToolCall
	card.Merge(toolCall)
	var input map[string]any
	_ = json.Unmarshal(card.RawInput, &input)
	switch card.Kind {
	case "execute":
		title = "Shell command"
	case "edit":
		title = "Edit file"
	case "delete":
		title = "Delete file"
	case "fetch":
		title = "Web fetch"
	case "search":
		title = "Web search"
	default:
		title = "Cursor permission"
	}
	prompt = strings.TrimSpace(card.Title)
	if match := cursorACPBackticks.FindStringSubmatch(prompt); match != nil {
		prompt = match[1]
	}
	var parts []string
	if card.Kind == "execute" && prompt != "" {
		parts = append(parts, prompt)
		prompt = "Run this command?"
	}
	if diff := cursorACPDiffs(card.Content); diff != "" {
		parts = append(parts, diff)
	}
	if text := cursorACPContentText(card.Content); text != "" {
		parts = append(parts, text)
	}
	if len(parts) == 0 && len(card.RawInput) > 0 {
		parts = append(parts, string(card.RawInput))
	}
	detail = strings.Join(parts, "\n\n")
	if prompt == "" {
		prompt = "Allow this Cursor action?"
	}
	return title, prompt, detail
}

// CursorACPTodos renders a todo list from Cursor's plan entries or its
// cursor/update_todos request as the checklist the terminal parser uses.
func CursorACPTodos(raw json.RawMessage) string {
	var todos []struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	}
	if json.Unmarshal(raw, &todos) != nil {
		return ""
	}
	lines := make([]string, 0, len(todos))
	for _, todo := range todos {
		if strings.TrimSpace(todo.Content) == "" {
			continue
		}
		lines = append(lines, cursorCLITodoMark(todo.Status)+" "+strings.TrimSpace(todo.Content))
	}
	return strings.Join(lines, "\n")
}
