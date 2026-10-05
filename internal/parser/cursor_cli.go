package parser

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Cursor's terminal CLI keeps each chat in a SQLite store,
// ~/.cursor/chats/<hash>/<chatId>/store.db, whose blobs table mixes protobuf
// conversation state with JSON messages in the AI SDK's shape:
//
//	{"role":"user","content":[{"type":"text","text":"<timestamp>…</timestamp>\n<user_query>…</user_query>"}]}
//	{"role":"assistant","content":[{"type":"reasoning",…},{"type":"text",…},
//	    {"type":"tool-call","toolCallId":"…","toolName":"Shell","args":{…}}]}
//	{"role":"tool","content":[{"type":"tool-result","toolCallId":"…","toolName":"Shell","result":"Exit code: 0…"}],
//	    "providerOptions":{"cursor":{"highLevelToolCallResult":{"output":{"success":{…}},"isError":false}}}}
//
// The result string is the text Cursor gave the model, wrapped in its own
// envelope ("Exit code: 0\n\nCommand output:\n\n```…```"). What the phone
// wants is in highLevelToolCallResult beside it: the exit code, stdout,
// a unified diff for every edit, the todo list, whether it was rejected. Every
// tool-result blob on the author's machine carries it.
//
// Several tools are not called directly: Cursor defers them behind
// CallDynamicTool {namespace, toolName, arguments} (TodoWrite, Delete,
// WebFetch, WebSearch, EditNotebook, Task, MCP tools …), found first through
// GetDynamicTools. Rows show the tool actually used.

// CursorCLIRow is one JSON blob from a chat store, in rowid order.
type CursorCLIRow struct {
	RowID int64
	ID    string
	Data  string
}

type cursorCLIBlob struct {
	Role            string          `json:"role"`
	Content         json.RawMessage `json:"content"`
	ProviderOptions struct {
		Cursor struct {
			HighLevel *cursorCLIOutcome `json:"highLevelToolCallResult"`
		} `json:"cursor"`
	} `json:"providerOptions"`
}

type cursorCLIPart struct {
	Type       string          `json:"type"`
	Text       string          `json:"text"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Args       json.RawMessage `json:"args"`
	Result     json.RawMessage `json:"result"`
	Content    json.RawMessage `json:"experimental_content"`
}

// cursorCLIOutcome is highLevelToolCallResult. Output is an object keyed by
// the outcome ("success", "failure", "error", "rejected" …), or an array of
// messages when a command was interrupted.
type cursorCLIOutcome struct {
	Output  json.RawMessage `json:"output"`
	IsError bool            `json:"isError"`
}

type cursorCLIResult struct {
	row     int64
	part    cursorCLIPart
	outcome *cursorCLIOutcome
}

// CursorCLIMessages turns store rows, oldest first, into the feed.
//
// A tool call becomes one row, settled in place by its result: the id comes
// from the call's toolCallId, so the running row and the finished one are
// the same message to the app. Results never make rows of their own. The
// caller may pass result rows from beyond the page so a call whose result is
// newer than the page still arrives settled; results without their call on
// the page are ignored, because the page that holds the call emits it.
func CursorCLIMessages(sessionID string, started int64, rows []CursorCLIRow) []protocol.Message {
	results := map[string]cursorCLIResult{}
	blobs := make([]cursorCLIBlob, len(rows))
	for i, row := range rows {
		if json.Unmarshal([]byte(row.Data), &blobs[i]) != nil || blobs[i].Role != "tool" {
			continue
		}
		for _, part := range cursorCLIParts(blobs[i].Content) {
			if part.Type != "tool-result" || part.ToolCallID == "" {
				continue
			}
			// Cursor rewrites a result in place for some tools (an image
			// read gains its description); the newest copy is the one.
			if previous, ok := results[part.ToolCallID]; ok && previous.row > row.RowID {
				continue
			}
			results[part.ToolCallID] = cursorCLIResult{row: row.RowID, part: part, outcome: blobs[i].ProviderOptions.Cursor.HighLevel}
		}
	}

	var out []protocol.Message
	for i, row := range rows {
		blob := blobs[i]
		if blob.Role != "user" && blob.Role != "assistant" {
			continue
		}
		base := protocol.Message{SessionID: sessionID, Ts: started + row.RowID}
		parts := cursorCLIParts(blob.Content)
		var texts []string
		var tools []protocol.Message
		for _, part := range parts {
			switch part.Type {
			case "text":
				if part.Text != "" {
					texts = append(texts, part.Text)
				}
			case "tool-call":
				if blob.Role != "assistant" || part.ToolCallID == "" {
					continue
				}
				msg := base
				msg.ID = CursorCLIToolID(part.ToolCallID)
				msg.Role = protocol.RoleTool
				result, done := results[part.ToolCallID]
				msg.Tool, msg.Text = cursorCLITool(part, result, done)
				tools = append(tools, msg)
			}
		}
		body := strings.TrimSpace(strings.Join(texts, "\n"))
		var lead []protocol.Message
		if blob.Role == "user" {
			if mode := cursorCLIModeChange(body); mode != "" {
				lead = append(lead, protocol.Message{
					ID: "cursor-cli:" + row.ID + ":mode", SessionID: sessionID, Ts: base.Ts,
					Role: protocol.RoleSystem, Text: mode,
				})
			}
			body = cursorCLIUserText(body)
		}
		if body != "" {
			msg := base
			msg.ID = "cursor-cli:" + row.ID
			msg.Role = protocol.Role(blob.Role)
			msg.Text = clipRunes(body, PreviewChars)
			lead = append(lead, msg)
		}
		out = append(out, lead...)
		out = append(out, tools...)
	}
	return out
}

// CursorCLIToolID is the stable message id of one tool call. Cursor's ids
// carry a newline ("call-…-0\nfc_…_0"), so they are hashed rather than used
// as they are.
func CursorCLIToolID(toolCallID string) string {
	sum := sha256.Sum256([]byte(toolCallID))
	return "cursor-cli:tool:" + hex.EncodeToString(sum[:12])
}

// CursorCLIPendingCalls lists the tool calls on rows whose result is not
// among them, so the caller can fetch those results from newer rows.
func CursorCLIPendingCalls(rows []CursorCLIRow) []string {
	called := map[string]bool{}
	var order []string
	for _, row := range rows {
		var blob cursorCLIBlob
		if json.Unmarshal([]byte(row.Data), &blob) != nil {
			continue
		}
		for _, part := range cursorCLIParts(blob.Content) {
			switch {
			case part.Type == "tool-call" && part.ToolCallID != "" && !called[part.ToolCallID]:
				called[part.ToolCallID] = true
				order = append(order, part.ToolCallID)
			case part.Type == "tool-result":
				delete(called, part.ToolCallID)
			}
		}
	}
	pending := order[:0]
	for _, id := range order {
		if called[id] {
			pending = append(pending, id)
		}
	}
	return pending
}

// CursorCLIModel reads the model that wrote an assistant blob.
func CursorCLIModel(data string) string {
	var blob struct {
		Role    string `json:"role"`
		Content []struct {
			ProviderOptions struct {
				Cursor struct {
					ModelName string `json:"modelName"`
				} `json:"cursor"`
			} `json:"providerOptions"`
		} `json:"content"`
	}
	if json.Unmarshal([]byte(data), &blob) != nil || blob.Role != "assistant" {
		return ""
	}
	for _, part := range blob.Content {
		if name := part.ProviderOptions.Cursor.ModelName; name != "" {
			return name
		}
	}
	return ""
}

func cursorCLIParts(raw json.RawMessage) []cursorCLIPart {
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return nil
		}
		return []cursorCLIPart{{Type: "text", Text: text}}
	}
	var parts []cursorCLIPart
	if json.Unmarshal(raw, &parts) != nil {
		return nil
	}
	return parts
}

var cursorCLIModeReminder = regexp.MustCompile(`You are now in (\w+) mode`)

// cursorCLIModeChange reads the reminder Cursor prepends to the first prompt
// after the mode changed ("<system_reminder> You are now in Plan mode…").
func cursorCLIModeChange(body string) string {
	reminder := extractTag(body, "system_reminder")
	if match := cursorCLIModeReminder.FindStringSubmatch(reminder); match != nil {
		return fmt.Sprintf("Switched to %s mode", match[1])
	}
	return ""
}

// cursorCLIUserText keeps what the person typed. Cursor wraps a prompt as
// "<timestamp>…</timestamp>\n<user_query>…</user_query>", may put an
// <image_files> or <system_reminder> block in front of it, and stores the
// generated workspace preamble (<user_info>) as a user message of its own.
func cursorCLIUserText(body string) string {
	if query := extractTag(body, "user_query"); query != "" {
		return query
	}
	trimmed := strings.TrimSpace(body)
	for _, scaffold := range []string{"<user_info>", "<system_reminder>", "<image_files>", "<timestamp>"} {
		if strings.HasPrefix(trimmed, scaffold) {
			return ""
		}
	}
	return trimmed
}

// cursorCLIDisplayNames renames tools whose Cursor name says less than what
// its own interface calls them.
var cursorCLIDisplayNames = map[string]string{
	"StrReplace":   "Edit",
	"MultiStrEdit": "Edit",
}

// cursorCLITool builds one tool row: its name and summary from the call, and,
// once the result exists, its status and output.
func cursorCLITool(call cursorCLIPart, result cursorCLIResult, done bool) (*protocol.Tool, string) {
	name, args := call.ToolName, call.Args
	namespace := ""
	if name == "CallDynamicTool" {
		var dynamic struct {
			Namespace string          `json:"namespace"`
			ToolName  string          `json:"toolName"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(call.Args, &dynamic) == nil && dynamic.ToolName != "" {
			name, args, namespace = dynamic.ToolName, dynamic.Arguments, dynamic.Namespace
		}
	}
	var input map[string]any
	_ = json.Unmarshal(args, &input)
	tool := &protocol.Tool{Name: name, Summary: cursorCLISummary(name, namespace, input), Status: protocol.ToolRunning}
	if display, ok := cursorCLIDisplayNames[name]; ok {
		tool.Name = display
	}
	if name == "" {
		tool.Name = "tool"
	}
	if !done {
		return tool, ""
	}
	status, text := cursorCLIOutcomeText(name, input, result)
	tool.Status = status
	return tool, text
}

func cursorCLIPick(input map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// cursorCLISummary is the one detail a row shows. The command for a shell
// call (not its description, which says what the agent hoped it would do),
// the absolute path for anything touching a file — which is also what lets
// the phone open an image the agent read — and the pattern for a search.
func cursorCLISummary(name, namespace string, input map[string]any) string {
	switch name {
	case "Shell":
		return ClipBlock(cursorCLIPick(input, "command"), CommandLines, CommandChars)
	case "Read", "Write", "StrReplace", "MultiStrEdit", "Delete", "EditNotebook":
		return cursorCLIPick(input, "path", "target_notebook", "file_path")
	case "Glob":
		return clip(cursorCLIPick(input, "glob_pattern", "pattern"), SummaryChars)
	case "Grep":
		return clip(cursorCLIPick(input, "pattern"), SummaryChars)
	case "LS", "ListDir":
		return cursorCLIPick(input, "target_directory", "path")
	case "WebFetch":
		return clip(cursorCLIPick(input, "url"), SummaryChars)
	case "WebSearch":
		return clip(cursorCLIPick(input, "search_term", "query"), SummaryChars)
	case "Task":
		return clip(cursorCLIPick(input, "description", "prompt"), SummaryChars)
	case "CreatePlan":
		return clip(cursorCLIPick(input, "name", "overview"), SummaryChars)
	case "SwitchMode":
		return clip(cursorCLIPick(input, "target_mode_id", "targetModeId"), SummaryChars)
	case "GenerateImage":
		return cursorCLIPick(input, "filename", "description")
	case "TodoWrite", "ReadTodos":
		return ""
	case "GetDynamicTools":
		parts := []string{cursorCLIPick(input, "namespace"), cursorCLIPick(input, "toolName"), cursorCLIPick(input, "pattern")}
		return clip(strings.Join(nonEmpty(parts), " · "), SummaryChars)
	case "ReadLints":
		if paths, ok := input["paths"].([]any); ok && len(paths) > 0 {
			if first, ok := paths[0].(string); ok {
				return first
			}
		}
		return ""
	}
	if namespace != "" && namespace != "cursor" {
		// An MCP tool: the server says where it went, the arguments what.
		compact, _ := json.Marshal(input)
		return clip(namespace+": "+string(compact), SummaryChars)
	}
	if direct := cursorCLIPick(input, "command", "path", "file_path", "query", "pattern", "url", "description"); direct != "" {
		return clip(direct, SummaryChars)
	}
	if len(input) == 0 {
		return ""
	}
	compact, _ := json.Marshal(input)
	return clip(string(compact), SummaryChars)
}

func nonEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

// cursorCLIOutcomeText reads a settled call's status and the output a phone
// should show for it.
func cursorCLIOutcomeText(name string, input map[string]any, result cursorCLIResult) (protocol.ToolStatus, string) {
	plain := cursorCLIResultString(result.part)
	outcome := result.outcome
	if outcome == nil {
		return protocol.ToolOK, ClipBlock(plain, PreviewLines, PreviewChars)
	}
	status := protocol.ToolOK
	if outcome.IsError {
		status = protocol.ToolError
	}
	// An interrupted command reports its messages as a bare array.
	var messages []string
	if json.Unmarshal(outcome.Output, &messages) == nil {
		return protocol.ToolError, ClipBlock(strings.Join(messages, "\n"), PreviewLines, PreviewChars)
	}
	var cases map[string]json.RawMessage
	if json.Unmarshal(outcome.Output, &cases) != nil {
		return status, ClipBlock(plain, PreviewLines, PreviewChars)
	}
	var fields struct {
		Stdout       string `json:"stdout"`
		Stderr       string `json:"stderr"`
		Interleaved  string `json:"interleavedOutput"`
		ExitCode     *int   `json:"exitCode"`
		Reason       string `json:"reason"`
		Error        string `json:"error"`
		ErrorMessage string `json:"errorMessage"`
		Content      string `json:"content"`
		DataBlobID   string `json:"dataBlobId"`
		Diff         string `json:"diffString"`
		Message      string `json:"message"`
		Path         string `json:"path"`
		PrevContent  string `json:"prevContent"`
		FilePath     string `json:"filePath"`
		Todos        []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	for _, key := range []string{"success", "failure", "error", "rejected"} {
		raw, ok := cases[key]
		if !ok {
			continue
		}
		_ = json.Unmarshal(raw, &fields)
		switch key {
		case "failure", "error", "rejected":
			status = protocol.ToolError
		}
		switch key {
		case "rejected":
			text := "Rejected"
			if fields.Reason != "" {
				text += ": " + fields.Reason
			}
			return status, text
		case "error":
			return status, ClipBlock(firstNonEmpty(fields.ErrorMessage, fields.Error, plain), PreviewLines, PreviewChars)
		}
		break
	}

	switch name {
	case "Shell":
		output := fields.Interleaved
		if output == "" {
			output = strings.TrimRight(fields.Stdout, "\n")
			if fields.Stderr != "" {
				output = strings.TrimSpace(output + "\n" + fields.Stderr)
			}
		}
		if fields.ExitCode != nil && *fields.ExitCode != 0 {
			status = protocol.ToolError
			output = strings.TrimSpace(fmt.Sprintf("Exit code %d\n%s", *fields.ExitCode, output))
		}
		return status, ClipBlock(output, PreviewLines, PreviewChars)
	case "Read":
		if fields.DataBlobID != "" || cursorCLIHasImage(result.part) {
			// The picture itself is on disk; the app opens it from the
			// row's path, and "[image]" is how every parser says so.
			return status, "[image]"
		}
		if fields.Content != "" {
			return status, ClipBlock(fields.Content, PreviewLines, PreviewChars)
		}
	case "Write", "StrReplace", "MultiStrEdit", "EditNotebook":
		if fields.Diff != "" {
			return status, ClipBlock(cursorCLIDiff(fields.Diff), PreviewLines, PreviewChars)
		}
		if fields.Message != "" {
			return status, fields.Message
		}
	case "Delete":
		if fields.PrevContent != "" {
			return status, ClipBlock(cursorCLIDeletionDiff(firstNonEmpty(fields.Path, cursorCLIPick(input, "path")), fields.PrevContent), PreviewLines, PreviewChars)
		}
	case "TodoWrite":
		if len(fields.Todos) > 0 {
			lines := make([]string, 0, len(fields.Todos))
			for _, todo := range fields.Todos {
				lines = append(lines, cursorCLITodoMark(todo.Status)+" "+todo.Content)
			}
			return status, strings.Join(lines, "\n")
		}
	case "CreatePlan":
		if plan := cursorCLIPick(input, "plan"); plan != "" {
			return status, ClipBlock(plan, PreviewLines, PreviewChars)
		}
	case "GetDynamicTools":
		// The tool catalogue the agent browsed: nothing a person reads.
		return status, ""
	case "GenerateImage":
		if fields.FilePath != "" {
			return status, "[image]"
		}
	}
	return status, ClipBlock(plain, PreviewLines, PreviewChars)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func cursorCLIResultString(part cursorCLIPart) string {
	var text string
	if json.Unmarshal(part.Result, &text) == nil {
		return text
	}
	return ""
}

func cursorCLIHasImage(part cursorCLIPart) bool {
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(part.Content, &blocks) != nil {
		return false
	}
	for _, block := range blocks {
		if block.Type == "image" {
			return true
		}
	}
	return false
}

// cursorCLIDiff normalises Cursor's diff headers, which join "a/" to an
// absolute path ("--- a//Users/…"), to the usual "--- a/Users/…".
func cursorCLIDiff(diff string) string {
	lines := strings.Split(diff, "\n")
	for i, line := range lines {
		for _, prefix := range []string{"--- a//", "+++ b//"} {
			if strings.HasPrefix(line, prefix) {
				lines[i] = prefix[:len(prefix)-1] + line[len(prefix):]
			}
		}
	}
	return strings.Join(lines, "\n")
}

// cursorCLIDeletionDiff shows a deleted file as the removal of its lines,
// which is what the app colours.
func cursorCLIDeletionDiff(path, content string) string {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n+++ /dev/null\n@@ -1,%d +0,0 @@", strings.TrimPrefix(path, "/"), len(lines))
	for _, line := range lines {
		out.WriteString("\n-" + line)
	}
	return out.String()
}

func cursorCLITodoMark(status string) string {
	switch strings.TrimPrefix(strings.ToLower(status), "todo_status_") {
	case "completed":
		return "✔"
	case "in_progress":
		return "◐"
	case "cancelled":
		return "✕"
	}
	return "○"
}

func clipRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
