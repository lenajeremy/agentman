package parser

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Kiro CLI writes each session to ~/.kiro/sessions/cli/<id>.jsonl, one event
// per line:
//
//	{"version":"v1","kind":"Prompt","data":{"message_id":…,"content":[…],"meta":{"timestamp":…}}}
//	{"version":"v1","kind":"AssistantMessage","data":{"message_id":…,"content":[…]}}
//	{"version":"v1","kind":"ToolResults","data":{"message_id":…,"content":[…],"results":{…}}}
//	{"version":"v1","kind":"Compaction","data":{"summary":…,"strategy":{…}}}
//	{"version":"v1","kind":"Clear","data":null}
//
// An assistant message carries its tool calls as toolUse blocks, and their
// results arrive later in a ToolResults event keyed by the same toolUseId.
// The binary also names ResetTo and CancelledPrompt records; neither was seen
// from the 2.27 interface (/rewind forks into a new file instead), so they are
// shown as plain notices.
//
// Every event is written once it is complete. A reply being streamed exists
// only on the screen until it is finished — see source/kiro_stream.go.
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
	// turnID is the latest prompt's id and turnTexts counts the reply texts
	// emitted since it. A reply text is named after its place in the turn
	// rather than after its own record — see textID.
	turnID    string
	turnTexts int
	calls     *boundedMap[kiroCall]
	// pending lists the calls still waiting for a result, oldest first.
	pending []string
	// todos is the latest task list, so a call that ticks off task "2" can
	// say which task that was.
	todos map[string]string
	// awaiting is true while the model owes the turn its next message: after
	// a prompt, or once every call it made has its result.
	awaiting bool
}

// kiroCall is a tool call remembered until its result arrives. The settled row
// replaces the running one by id, so it keeps the call's timestamp: taking a
// new one would make the row jump down the feed when the command finished.
type kiroCall struct {
	tool string // Kiro's own name: "read", "shell", "todo_list"
	ts   int64
	rows []kiroRow
	// diff is a file edit as a unified diff, shown while the call waits for
	// approval and kept once it succeeds.
	diff string
	// base is the directory a search ran in, for shortening its results.
	base string
	// report stands in for a result that carries no text of its own: a
	// subagent hands its answer over in the call, and the result is empty.
	report string
}

// kiroRow is one feed row a call produces. A batched read becomes a row per
// file, the way Claude reads one file per call, so every file has its own
// preview and every image can be opened.
type kiroRow struct {
	id      string
	name    string
	summary string
}

// maxKiroPending bounds the calls remembered as unanswered. A turn rarely has
// more than a handful in flight; this only stops a damaged transcript growing
// the list without end.
const maxKiroPending = 2000

// NewKiroParser creates a parser bound to one session. Feed it lines in file
// order from the start of the transcript.
func NewKiroParser(sessionID string) *KiroParser {
	return &KiroParser{
		sessionID: sessionID,
		calls:     newBoundedMap[kiroCall](maxKiroPending),
		todos:     map[string]string{},
	}
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
		// A new prompt means the last turn is over. A call that never got a
		// result was abandoned with it — Kiro was closed mid-call — and would
		// otherwise spin as "running" for the rest of the feed.
		out := p.abandonPending()
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
		p.turnID, p.turnTexts = event.Data.MessageID, 0
		p.awaiting = true
		text := kiroPromptText(event.Data.Content)
		if text == "" {
			return out
		}
		return append(out, protocol.Message{
			ID: id, SessionID: p.sessionID, Role: protocol.RoleUser, Ts: ts, Text: text,
		})

	case "AssistantMessage":
		// A message is written whole; whatever comes next is a call's result
		// or, with no calls, the end of the turn.
		p.awaiting = false
		var out []protocol.Message
		for i, block := range event.Data.Content {
			switch block.Kind {
			case "text":
				var text string
				if json.Unmarshal(block.Data, &text) != nil {
					continue
				}
				if text = strings.TrimSpace(kiroUnwrapSteeringNotes(text)); text == "" {
					continue
				}
				role := protocol.RoleAssistant
				if kiroNotices[text] {
					role = protocol.RoleSystem
				}
				out = append(out, protocol.Message{
					ID: p.textID(id, i), SessionID: p.sessionID, Role: role, Ts: p.next(), Text: text,
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
				call := p.describeCall(callID, use.Name, use.Input)
				call.ts = p.next()
				p.calls.set(callID, call)
				p.pending = append(p.pending, callID)
				if len(p.pending) > maxKiroPending {
					p.pending = p.pending[1:]
				}
				for _, row := range call.rows {
					out = append(out, protocol.Message{
						ID: row.id, SessionID: p.sessionID, Role: protocol.RoleTool, Ts: call.ts,
						Text: call.diff,
						Tool: &protocol.Tool{Name: row.name, Summary: row.summary, Status: protocol.ToolRunning},
					})
				}
			}
			// Thinking arrives redacted — an opaque byte array — so there is
			// nothing in it to show.
		}
		return out

	case "ToolResults":
		defer func() { p.awaiting = len(p.pending) == 0 }()
		var out []protocol.Message
		for i, block := range event.Data.Content {
			switch block.Kind {
			case "toolResult":
				var result kiroToolResult
				if json.Unmarshal(block.Data, &result) != nil || result.ToolUseID == "" {
					continue
				}
				out = append(out, p.settle(result)...)
			case "text":
				// A message typed while the agent works is handed to it inside
				// the next tool results, wrapped in instructions for the model.
				// It is still something the user said, and dropping it left the
				// reply answering a message the feed never showed.
				var text string
				if json.Unmarshal(block.Data, &text) != nil {
					continue
				}
				for j, steer := range kiroSteering(text) {
					steerID := steer.id
					if steerID == "" {
						steerID = fmt.Sprintf("%s:%d:%d", id, i, j)
					}
					out = append(out, protocol.Message{
						ID: steerID, SessionID: p.sessionID, Role: protocol.RoleUser, Ts: p.next(), Text: steer.text,
					})
				}
			}
		}
		return out

	case "Compaction", "Clear", "ResetTo", "CancelledPrompt":
		p.awaiting = false
		return []protocol.Message{{
			ID: fmt.Sprintf("o%d", offset), SessionID: p.sessionID, Role: protocol.RoleSystem,
			Ts: p.next(), Text: kiroEventNotices[event.Kind],
		}}
	}
	return nil
}

// kiroEventNotices are the records that change the conversation rather than
// add to it.
var kiroEventNotices = map[string]string{
	"Compaction":      "Context compacted",
	"Clear":           "Conversation cleared",
	"ResetTo":         "Conversation rewound",
	"CancelledPrompt": "Prompt cancelled",
}

// kiroNotices are replies Kiro writes in the agent's name when the user
// stopped it. They are not the agent's words, so they read as notices.
var kiroNotices = map[string]bool{
	"Response was interrupted by the user":                         true,
	"Tool uses were interrupted, waiting for the next user prompt": true,
}

// textID names a reply text after its place in the turn: the prompt's id and
// how many texts came before it. A record's own id is a random UUID minted
// when the reply finishes, and the reply is on screen long before that; a
// name the streaming preview can know in advance is what lets the finished
// text replace the preview in place instead of arriving beneath it. Forked
// sessions keep their message ids, so the name survives a /rewind too.
//
// Before the first prompt — a window that starts mid-turn — there is no turn
// to count in, and the record's own id is all there is.
func (p *KiroParser) textID(recordID string, index int) string {
	if p.turnID == "" {
		return fmt.Sprintf("%s:%d", recordID, index)
	}
	p.turnTexts++
	return KiroTextID(p.turnID, p.turnTexts)
}

// KiroTextID is the id of the n-th reply text after the prompt promptID,
// counting from one.
func KiroTextID(promptID string, n int) string {
	return promptID + "/t" + strconv.Itoa(n)
}

// TurnState reports the latest prompt's id, how many reply texts have
// followed it, and a timestamp that sorts after everything emitted so far —
// what a streaming preview needs to name the text Kiro is writing now and to
// place it below what is already shown.
func (p *KiroParser) TurnState() (promptID string, texts int, nextTs int64) {
	return p.turnID, p.turnTexts, p.turnTs + p.step + 1
}

// AwaitingReply reports whether the model is writing the turn's next message:
// the newest record is a prompt, or results for every call it made. That is
// exactly when Kiro's screen shows text its transcript does not have yet.
func (p *KiroParser) AwaitingReply() bool {
	return p.awaiting
}

// abandonPending settles every call still waiting for a result as failed.
func (p *KiroParser) abandonPending() []protocol.Message {
	var out []protocol.Message
	for _, callID := range p.pending {
		call, ok := p.calls.get(callID)
		if !ok {
			continue
		}
		for i, row := range call.rows {
			text := ""
			if i == 0 {
				text = "No result: the session stopped before this finished."
			}
			out = append(out, protocol.Message{
				ID: row.id, SessionID: p.sessionID, Role: protocol.RoleTool, Ts: call.ts, Text: text,
				Tool: &protocol.Tool{Name: row.name, Summary: row.summary, Status: protocol.ToolError},
			})
		}
	}
	p.pending = nil
	return out
}

// settle re-emits a call's rows with its result.
func (p *KiroParser) settle(result kiroToolResult) []protocol.Message {
	for i, callID := range p.pending {
		if callID == result.ToolUseID {
			p.pending = append(p.pending[:i:i], p.pending[i+1:]...)
			break
		}
	}
	call, known := p.calls.get(result.ToolUseID)
	if !known {
		// The call scrolled out of the bounded map. A result with no name to
		// show is still better than a row that stays running.
		call = kiroCall{ts: p.next(), rows: []kiroRow{{id: result.ToolUseID, name: "Tool"}}}
	}
	status := protocol.ToolOK
	if result.Status != "" && !strings.EqualFold(result.Status, "success") {
		status = protocol.ToolError
	}

	// One preview per row when the result has a block per row — a batched
	// read answers each file in turn — otherwise everything on the first.
	previews := make([]string, len(call.rows))
	if len(call.rows) > 1 && len(result.Content) == len(call.rows) {
		for i, block := range result.Content {
			text, failed := p.resultText(call, []kiroBlock{block})
			previews[i] = text
			if failed {
				status = protocol.ToolError
			}
		}
	} else {
		text, failed := p.resultText(call, result.Content)
		previews[0] = text
		if failed {
			status = protocol.ToolError
		}
	}
	if strings.TrimSpace(previews[0]) == "" {
		previews[0] = call.report
	}
	// An edit's diff says more than "Successfully replaced 1 occurrence(s)",
	// so it stays. A failure says why, and that is what matters then.
	if call.diff != "" && status == protocol.ToolOK {
		previews[0] = call.diff
	}

	out := make([]protocol.Message, 0, len(call.rows))
	for i, row := range call.rows {
		out = append(out, protocol.Message{
			ID: row.id, SessionID: p.sessionID, Role: protocol.RoleTool, Ts: call.ts,
			Text: ClipBlock(previews[i], PreviewLines, PreviewChars),
			Tool: &protocol.Tool{Name: row.name, Summary: row.summary, Status: status},
		})
	}
	return out
}

// kiroPromptText joins a prompt's text, marking each picture attached to it.
// Kiro attaches an image itself when a prompt names one by path, which is how
// a photo sent from the phone arrives; "[image]" is how every feed in the app
// shows that one was there.
func kiroPromptText(blocks []kiroBlock) string {
	var parts []string
	for _, block := range blocks {
		switch block.Kind {
		case "text":
			var text string
			if json.Unmarshal(block.Data, &text) == nil && strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
		case "image":
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "\n\n")
}

// kiroSteer is one message the user typed while the agent was working.
type kiroSteer struct {
	id   string
	text string
}

// kiroSteeringMessage finds each message in Kiro's steering wrapper:
//
//	[LIVE STEERING - New message from user]
//	…
//	<user_message id="steer-b8ce6471f8034f67acd1b9147f2b4502">
//	Steer: make the haiku about phones instead.
//	</user_message>
var kiroSteeringMessage = regexp.MustCompile(`(?s)<user_message id="([^"]*)">\s*(.*?)\s*</user_message>`)

func kiroSteering(text string) []kiroSteer {
	if !strings.Contains(text, "[LIVE STEERING") {
		return nil
	}
	var out []kiroSteer
	for _, match := range kiroSteeringMessage.FindAllStringSubmatch(text, -1) {
		if body := strings.TrimSpace(match[2]); body != "" {
			out = append(out, kiroSteer{id: match[1], text: body})
		}
	}
	return out
}

// kiroUnwrapSteeringNotes replaces the note Kiro asks the agent to append
// after a steering message — "[STEERING steer-<id>: what I did about it]" —
// with the note itself, which is how Kiro's own interface shows it.
func kiroUnwrapSteeringNotes(text string) string {
	const open = "[STEERING steer-"
	for {
		start := strings.LastIndex(text, open)
		if start < 0 {
			return text
		}
		rest := text[start+len(open):]
		colon := strings.Index(rest, ":")
		end := strings.LastIndex(rest, "]")
		if colon < 0 || end < colon {
			return text
		}
		text = text[:start] + strings.TrimSpace(rest[colon+1:end]) + rest[end+1:]
	}
}

// describeCall decides how a call reads in the feed: the name the app knows
// how to render, the part of the input worth a glance, and — for a batched
// read — one row per file.
func (p *KiroParser) describeCall(callID, tool string, raw json.RawMessage) kiroCall {
	var input map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &input)
	}
	name := kiroToolName(tool)
	call := kiroCall{tool: tool}
	row := kiroRow{id: callID, name: name}

	switch name {
	case "Shell":
		row.summary = ClipBlock(kiroString(input, "command"), CommandLines, CommandChars)
	case "Read":
		if rows := kiroReadRows(callID, input); len(rows) > 0 {
			call.rows = rows
			return call
		}
		row.summary = clip(kiroString(input, "path", "file_path"), SummaryChars)
	case "Write":
		// The path alone, never the edit kind: fs_write carries a "command"
		// field too ("create", "strReplace"), and a generic "command first"
		// rule labelled every file write with the word "create".
		row.summary = clip(kiroString(input, "path", "file_path", "filePath"), SummaryChars)
		switch kiroString(input, "command") {
		case "strReplace", "str_replace", "insert":
			// Claude calls a change to an existing file "Edit"; the app draws
			// the two alike, and the word says which one this was.
			row.name = "Edit"
		}
		call.diff = kiroWriteDiff(input)
	case "Grep", "Glob":
		row.summary = clip(kiroString(input, "pattern"), SummaryChars)
		call.base = kiroString(input, "path")
	case "Todo list":
		row.summary = clip(p.todoSummary(input), SummaryChars)
	case "Subagent":
		row.summary = clip(kiroSubagentTask(input), SummaryChars)
	case "Summary":
		row.summary = clip(kiroString(input, "taskDescription"), SummaryChars)
		call.report = kiroString(input, "taskResult")
	case "AWS":
		row.summary = clip(strings.TrimSpace(kiroString(input, "service_name")+" "+
			kiroString(input, "operation_name")), SummaryChars)
	default:
		row.summary = clip(kiroString(input, "path", "file_path", "filePath", "query", "pattern", "url",
			"symbol_name", "doc_path", "name", "command"), SummaryChars)
	}
	if row.summary == "" {
		// Every call states why it is being made; better that than nothing.
		row.summary = clip(kiroString(input, "__tool_use_purpose"), SummaryChars)
	}
	call.rows = []kiroRow{row}
	return call
}

// kiroReadRows turns a read's operations into one row per file, directory or
// image. The first keeps the call's id, so a result settles it like any other.
//
// Each row's summary is the bare path, which is what lets the app open an
// image the agent looked at and the daemon record the file as one it read.
func kiroReadRows(callID string, input map[string]any) []kiroRow {
	operations, _ := input["operations"].([]any)
	var rows []kiroRow
	add := func(name, path string) {
		id := callID
		if len(rows) > 0 {
			id = callID + "#" + strconv.Itoa(len(rows)+1)
		}
		rows = append(rows, kiroRow{id: id, name: name, summary: clip(path, SummaryChars)})
	}
	for _, raw := range operations {
		op, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch kiroString(op, "mode") {
		case "Image":
			images, _ := op["image_paths"].([]any)
			for _, image := range images {
				if path, ok := image.(string); ok && strings.TrimSpace(path) != "" {
					add("Read", path)
				}
			}
		case "Directory":
			if path := kiroString(op, "path"); path != "" {
				add("List", path)
			}
		default:
			if path := kiroString(op, "path", "file_path"); path != "" {
				add("Read", path)
			}
		}
	}
	return rows
}

// resultText flattens a tool result for the preview, and reports whether a
// command exited unsuccessfully. Kiro marks the call itself a success when the
// command ran at all, so a failing command is only visible in its exit status.
func (p *KiroParser) resultText(call kiroCall, blocks []kiroBlock) (string, bool) {
	var parts []string
	failed := false
	for _, block := range blocks {
		switch block.Kind {
		case "text":
			var text string
			if json.Unmarshal(block.Data, &text) == nil && text != "" {
				parts = append(parts, text)
			}
		case "image":
			parts = append(parts, "[image]")
		case "json":
			text, bad := p.jsonResult(call, block.Data)
			failed = failed || bad
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n"), failed
}

// jsonResult renders the structured results of shell, grep, glob and the task
// list as the lines a person would read, rather than as the JSON they are.
func (p *KiroParser) jsonResult(call kiroCall, raw json.RawMessage) (string, bool) {
	var shell struct {
		ExitStatus string `json:"exit_status"`
		Stdout     string `json:"stdout"`
		Stderr     string `json:"stderr"`
	}
	if json.Unmarshal(raw, &shell) == nil && (shell.ExitStatus != "" || shell.Stdout != "" || shell.Stderr != "") {
		failed := shell.ExitStatus != "" && !strings.HasSuffix(shell.ExitStatus, " 0")
		var parts []string
		for _, stream := range []string{shell.Stdout, shell.Stderr} {
			if strings.TrimSpace(stream) != "" {
				parts = append(parts, strings.TrimRight(stream, "\n"))
			}
		}
		return strings.Join(parts, "\n"), failed
	}

	switch kiroToolName(call.tool) {
	case "Grep":
		var grep struct {
			Truncated bool `json:"truncated"`
			Results   []struct {
				File    string   `json:"file"`
				Count   int      `json:"count"`
				Matches []string `json:"matches"`
			} `json:"results"`
		}
		if json.Unmarshal(raw, &grep) == nil && grep.Results != nil {
			var lines []string
			for _, result := range grep.Results {
				file := kiroRelative(call.base, result.File)
				if len(result.Matches) == 0 {
					lines = append(lines, fmt.Sprintf("%s (%d)", file, result.Count))
					continue
				}
				for _, match := range result.Matches {
					lines = append(lines, file+":"+match)
				}
			}
			if len(lines) == 0 {
				lines = append(lines, "No matches")
			}
			if grep.Truncated {
				lines = append(lines, "… more matches not shown")
			}
			return strings.Join(lines, "\n"), false
		}
	case "Glob":
		var glob struct {
			FilePaths []string `json:"filePaths"`
			Truncated bool     `json:"truncated"`
		}
		if json.Unmarshal(raw, &glob) == nil && glob.FilePaths != nil {
			lines := make([]string, 0, len(glob.FilePaths)+1)
			for _, path := range glob.FilePaths {
				lines = append(lines, kiroRelative(call.base, path))
			}
			if len(lines) == 0 {
				lines = append(lines, "No files")
			}
			if glob.Truncated {
				lines = append(lines, "… more files not shown")
			}
			return strings.Join(lines, "\n"), false
		}
	case "Todo list":
		var list struct {
			Description string `json:"description"`
			Tasks       []struct {
				ID          string `json:"id"`
				Description string `json:"task_description"`
				Completed   bool   `json:"completed"`
			} `json:"tasks"`
		}
		if json.Unmarshal(raw, &list) == nil && list.Tasks != nil {
			p.todos = map[string]string{}
			// Kiro empties the list once its last task is done.
			if len(list.Tasks) == 0 {
				return "No tasks left", false
			}
			lines := make([]string, 0, len(list.Tasks)+1)
			if list.Description != "" {
				lines = append(lines, list.Description)
			}
			// "[x]" rather than a ballot-box glyph: iOS draws those as emoji,
			// which sit off the baseline of a monospace preview.
			for _, task := range list.Tasks {
				p.todos[task.ID] = task.Description
				mark := "[ ]"
				if task.Completed {
					mark = "[x]"
				}
				lines = append(lines, mark+" "+task.Description)
			}
			return strings.Join(lines, "\n"), false
		}
	}
	// Some other tool's structured result: show it rather than drop it.
	return string(raw), false
}

// todoSummary says what a task-list call does: the list it creates, or the
// tasks it ticks off or removes, by name when an earlier result named them.
func (p *KiroParser) todoSummary(input map[string]any) string {
	names := func(key string) string {
		ids, _ := input[key].([]any)
		var out []string
		for _, raw := range ids {
			id := fmt.Sprint(raw)
			if name := p.todos[id]; name != "" {
				out = append(out, name)
			} else {
				out = append(out, "task "+id)
			}
		}
		return strings.Join(out, ", ")
	}
	switch kiroString(input, "command") {
	case "create":
		return kiroString(input, "task_list_description", "todo_list_description")
	case "complete":
		if done := names("completed_task_ids"); done != "" {
			return "Done: " + done
		}
	case "remove":
		if removed := names("remove_task_ids"); removed != "" {
			return "Removed: " + removed
		}
	}
	return ""
}

// kiroSubagentTask is what a subagent call was asked to do.
func kiroSubagentTask(input map[string]any) string {
	if task := kiroString(input, "task", "content", "query"); task != "" {
		return task
	}
	stages, _ := input["stages"].([]any)
	for _, raw := range stages {
		if stage, ok := raw.(map[string]any); ok {
			if prompt := kiroString(stage, "prompt_template", "name"); prompt != "" {
				return prompt
			}
		}
	}
	return ""
}

// kiroWriteDiff renders a file edit as a unified diff, which the app colours.
//
// The transcript records the text, not where it went: a replacement has no
// line number at all. Lines are numbered from the start of the changed
// passage, as Kiro's own approval screen numbers them — except for a new
// file, where that is also the truth.
func kiroWriteDiff(input map[string]any) string {
	lines := func(text string) []string {
		if text == "" {
			return nil
		}
		return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	}
	hunk := func(oldStart int, removed []string, newStart int, added []string) string {
		if len(removed) == 0 && len(added) == 0 {
			return ""
		}
		var b strings.Builder
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@", oldStart, len(removed), newStart, len(added))
		for _, line := range removed {
			b.WriteString("\n-" + line)
		}
		for _, line := range added {
			b.WriteString("\n+" + line)
		}
		return ClipBlock(b.String(), PreviewLines, PreviewChars)
	}
	switch kiroString(input, "command") {
	case "create":
		return hunk(0, nil, 1, lines(kiroString(input, "content", "file_text")))
	case "strReplace", "str_replace":
		return hunk(1, lines(kiroString(input, "oldStr", "old_str")), 1,
			lines(kiroString(input, "newStr", "new_str")))
	case "insert":
		added := lines(kiroString(input, "content", "newStr", "new_str"))
		// insertLine is 0-indexed, and the text goes in after that line.
		for _, key := range []string{"insertLine", "insert_line"} {
			if at, ok := input[key].(float64); ok && at >= 0 {
				return hunk(int(at), nil, int(at)+1, added)
			}
		}
		return hunk(1, nil, 1, added)
	}
	return ""
}

// kiroRelative shortens a path a search found to the part below where the
// search ran, which is all a phone row has room for.
func kiroRelative(base, path string) string {
	if base == "" {
		return path
	}
	if rel, err := filepath.Rel(base, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

// kiroString returns the first of keys holding a non-blank string.
func kiroString(from map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := from[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// kiroToolName gives Kiro's built-in tools the names the app already knows how
// to render: a "Shell" row shows its command, a "Read" row can open an image.
// The names of Kiro's first engine (fs_read, execute_bash, use_subagent …)
// are kept so an older transcript reads the same.
func kiroToolName(name string) string {
	switch name {
	case "shell", "execute_bash", "execute_cmd":
		return "Shell"
	case "read", "fs_read":
		return "Read"
	case "write", "fs_write":
		return "Write"
	case "grep":
		return "Grep"
	case "glob":
		return "Glob"
	case "web_search":
		return "Web search"
	case "web_fetch":
		return "Web fetch"
	case "todo_list":
		return "Todo list"
	case "subagent", "use_subagent", "delegate":
		return "Subagent"
	case "summary":
		return "Summary"
	case "introspect":
		return "Introspect"
	case "knowledge":
		return "Knowledge"
	case "code":
		return "Code"
	case "use_aws":
		return "AWS"
	case "goal":
		return "Goal"
	case "":
		return "Tool"
	}
	return name
}
