package parser

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Scaffolding the CLI injects as though the user had typed it. A command's
// own records match too; commandRows reads those first.
var claudeCommandWrapper = regexp.MustCompile(
	`^\s*<(command-name|command-message|command-args|local-command-stdout|local-command-stderr|user-prompt-submit-hook)`)

// A command the person ran is two records: what they typed, as its name and
// arguments, and then what it printed. A line run in shell mode ("!") is the
// same pair under other tags.
var (
	claudeCommandName = regexp.MustCompile(`<command-name>([^<]*)</command-name>`)
	claudeCommandArgs = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	claudeCommandOut  = regexp.MustCompile(`(?s)^\s*<local-command-(stdout|stderr)>(.*?)</local-command-(?:stdout|stderr)>\s*$`)
	claudeShellIn     = regexp.MustCompile(`(?s)^\s*<bash-input>(.*?)</bash-input>\s*$`)
	claudeShellOut    = regexp.MustCompile(`(?s)<bash-(stdout|stderr)>(.*?)</bash-(?:stdout|stderr)>`)
)

// commandRows reads a record that is one half of a command, and whether it
// was. What was typed is the person's line, as the terminal shows it; what it
// printed is a step titled with its first line, opening to the rest.
func (p *ClaudeParser) commandRows(body, id string, ts int64, sidechain bool) ([]protocol.Message, bool) {
	typed := func(text string) []protocol.Message {
		return []protocol.Message{{ID: id, SessionID: p.sessionID, Role: protocol.RoleUser, Ts: ts, Text: text, IsSidechain: sidechain}}
	}
	printed := func(name, output string, failed bool) []protocol.Message {
		lines := blockLines(output)
		if len(lines) == 0 {
			return nil
		}
		status := protocol.ToolOK
		if failed {
			status = protocol.ToolError
		}
		row := protocol.Message{
			ID: id, SessionID: p.sessionID, Role: protocol.RoleTool, Ts: ts, IsSidechain: sidechain,
			Tool: &protocol.Tool{Name: name, Status: status, Title: clip(lines[0], 200)},
		}
		// One line is all title; more opens to the whole of it.
		if len(lines) > 1 {
			row.Text = ClipBlock(output, PreviewLines, PreviewChars)
		}
		return []protocol.Message{row}
	}

	if m := claudeCommandName.FindStringSubmatch(body); m != nil {
		name := strings.TrimSpace(m[1])
		if name == "" {
			return nil, true
		}
		if !strings.HasPrefix(name, "/") {
			name = "/" + name
		}
		if args := claudeCommandArgs.FindStringSubmatch(body); args != nil && strings.TrimSpace(args[1]) != "" {
			name += " " + strings.TrimSpace(args[1])
		}
		return typed(name), true
	}
	if m := claudeCommandOut.FindStringSubmatch(body); m != nil {
		return printed("Command", m[2], m[1] == "stderr"), true
	}
	if m := claudeShellIn.FindStringSubmatch(body); m != nil {
		if line := strings.TrimSpace(m[1]); line != "" {
			return typed("!" + line), true
		}
		return nil, true
	}
	if outs := claudeShellOut.FindAllStringSubmatch(body, -1); outs != nil {
		var stdout, stderr string
		for _, out := range outs {
			if out[1] == "stderr" {
				stderr = out[2]
			} else {
				stdout = out[2]
			}
		}
		// What went wrong is the part worth reading when nothing else came out.
		if strings.TrimSpace(stdout) == "" {
			return printed("Bash", stderr, strings.TrimSpace(stderr) != ""), true
		}
		if strings.TrimSpace(stderr) != "" {
			stdout += "\n" + stderr
		}
		return printed("Bash", stdout, false), true
	}
	return nil, false
}

// claudeTaskNotice is the note Claude Code hands the agent when a background
// task ends. It arrives as though the user had typed it, XML and all; it is
// shown as a quiet notice saying what finished.
var (
	claudeTaskNotice  = regexp.MustCompile(`^\s*<task-notification>`)
	claudeTaskSummary = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)
)

// taskNotice is the line to show for a background task's note, and whether
// the text was one.
func taskNotice(body string) (string, bool) {
	if !claudeTaskNotice.MatchString(body) {
		return "", false
	}
	if m := claudeTaskSummary.FindStringSubmatch(body); m != nil {
		if summary := strings.Join(strings.Fields(m[1]), " "); summary != "" {
			return summary, true
		}
	}
	return "A background task finished", true
}

// Claude Code's own marks in a prompt pasted into it, as an image sent from
// the Mac with a message of several lines is: the paste wrapped in
// pasted_content tags, and a picture it took in replaced by "[Image #N]",
// with the file it came from named in a record of its own that follows.
var (
	claudePasteOpen   = regexp.MustCompile(`<pasted_content[^>]*>\n?`)
	claudePasteClose  = regexp.MustCompile(`\n?</pasted_content[^>]*>`)
	claudeImageMark   = regexp.MustCompile(`\[Image #\d+\]`)
	claudeImageSource = regexp.MustCompile(`\[Image: source: ([^\]\n]+)\]`)
)

// claudeRecord is the subset of a transcript line we rely on.
type claudeRecord struct {
	Type        string         `json:"type"`
	UUID        string         `json:"uuid"`
	PromptID    string         `json:"promptId"`
	Timestamp   string         `json:"timestamp"`
	IsMeta      bool           `json:"isMeta"`
	IsSidechain bool           `json:"isSidechain"`
	Message     *claudeMessage `json:"message"`
	// A "system" record's notice. Content is a bare string there; it is kept
	// raw so a record of another type that uses the key differently still
	// decodes.
	Subtype string          `json:"subtype"`
	Level   string          `json:"level"`
	Content json.RawMessage `json:"content"`
	// Attachment is what an "attachment" record carries. One kind matters
	// here: a message the user sent while Claude was working, which it took
	// in the middle of a turn rather than as a new one.
	Attachment *claudeAttachment `json:"attachment"`
}

type claudeAttachment struct {
	Type string `json:"type"`
	// Prompt is the message: a string, or content blocks.
	Prompt      json.RawMessage `json:"prompt"`
	CommandMode string          `json:"commandMode"`
	IsMeta      bool            `json:"isMeta"`
	Origin      struct {
		Kind string `json:"kind"`
	} `json:"origin"`
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
	// toolCalls holds calls seen before their result — the live-tail case.
	toolCalls *boundedMap[toolCall]
	// awaiting holds results whose call this parser has not met yet. Reading
	// backwards that call is still ahead, and a page that stops before it
	// would hand it to a page whose parser never saw the result.
	awaiting map[string]struct{}
	// prompts holds a prompt whose pictures Claude Code replaced with
	// "[Image #N]", by its promptId, until the record naming their files
	// follows and it is emitted again with them. sources holds those files,
	// met first when reading backwards.
	prompts *boundedMap[[]protocol.Message]
	sources *boundedMap[[]string]
}

// maxAwaitingCalls bounds awaiting for a parser that only ever meets results:
// a live tail can see results for calls written before it started.
const maxAwaitingCalls = 256

// AwaitingCalls reports whether a result has been met whose call has not.
// Paging uses it to keep reading back until those calls are reached, so they
// settle on the same page. See jsonl.BackwardOptions.Unsettled.
func (p *ClaudeParser) AwaitingCalls() bool { return len(p.awaiting) > 0 }

// NewClaudeParser creates a parser bound to one session.
func NewClaudeParser(sessionID string) *ClaudeParser {
	return &ClaudeParser{
		sessionID: sessionID,
		outcomes:  newBoundedMap[toolOutcome](2000),
		toolCalls: newBoundedMap[toolCall](2000),
		awaiting:  map[string]struct{}{},
		prompts:   newBoundedMap[[]protocol.Message](64),
		sources:   newBoundedMap[[]string](64),
	}
}

// Parse implements Parser.
func (p *ClaudeParser) Parse(line string, offset int64) []protocol.Message {
	var rec claudeRecord
	if !decode(line, &rec) {
		return nil
	}
	if rec.Type == "system" {
		return p.notice(rec, offset)
	}
	if rec.Type == "attachment" {
		return p.takenMidTurn(rec, offset)
	}
	if rec.Type != "user" && rec.Type != "assistant" {
		return nil
	}
	if rec.IsMeta {
		return p.imageSources(rec)
	}
	if rec.Message == nil {
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
		if role == protocol.RoleUser {
			if rows, ok := p.commandRows(body, uuid, ts, rec.IsSidechain); ok {
				return rows
			}
		}
		if body == "" || claudeCommandWrapper.MatchString(body) {
			return nil
		}
		if notice, ok := taskNotice(body); ok && role == protocol.RoleUser {
			return []protocol.Message{{
				ID: uuid, SessionID: p.sessionID, Role: protocol.RoleSystem, Ts: ts,
				Text: notice, IsSidechain: rec.IsSidechain,
			}}
		}
		return p.tidyPrompt(rec.PromptID, []protocol.Message{{
			ID: uuid, SessionID: p.sessionID, Role: role, Ts: ts,
			Text: body, IsSidechain: rec.IsSidechain,
		}})
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
			if notice, ok := taskNotice(body); ok && role == protocol.RoleUser {
				out = append(out, protocol.Message{
					ID: fmt.Sprintf("%s:%d", uuid, i), SessionID: p.sessionID,
					Role: protocol.RoleSystem, Ts: ts, Text: notice, IsSidechain: rec.IsSidechain,
				})
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
			summary := summarizeToolInput(name, block.Input)
			// The agent's own words for the step, when it gave them.
			title := ""
			if own := toolDescription(block.Input); own != "" {
				title = DescribeTool(name, summary, own)
			}
			p.toolCalls.set(id, toolCall{name: name, summary: summary, title: title})
			delete(p.awaiting, id)

			msg := protocol.Message{
				ID: id, SessionID: p.sessionID, Role: protocol.RoleTool, Ts: ts,
				Tool: &protocol.Tool{
					Name:    name,
					Summary: summary,
					Status:  protocol.ToolRunning,
					Title:   title,
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
			outcome := toolOutcome{status: status, preview: ClipBlock(flattenClaudeResult(block.Content), PreviewLines, PreviewChars)}
			p.outcomes.set(block.ToolUseID, outcome)
			if _, known := p.toolCalls.get(block.ToolUseID); !known && len(p.awaiting) < maxAwaitingCalls {
				p.awaiting[block.ToolUseID] = struct{}{}
			}

			// Reading forwards the row already exists, so re-emit it under the
			// same ID to settle it. The app upserts by ID, so this replaces the
			// "running" row rather than adding a second one.
			if call, ok := p.toolCalls.get(block.ToolUseID); ok {
				out = append(out, protocol.Message{
					ID: block.ToolUseID, SessionID: p.sessionID,
					Role: protocol.RoleTool, Ts: ts, Text: outcome.preview,
					// Carry the summary through: this row replaces the running
					// one by id, so omitting it erases the command.
					Tool:        &protocol.Tool{Name: call.name, Summary: call.summary, Status: status, Title: call.title},
					IsSidechain: rec.IsSidechain,
				})
			}

			// "thinking" blocks are deliberately dropped: long, mostly opaque
			// signature payload, and not what a glance at a phone is for.
		}
	}
	return p.tidyPrompt(rec.PromptID, out)
}

// tidyPrompt takes Claude Code's marks out of what the user sent: a paste's
// tags, and each "[Image #N]" put back as the file it was once that is
// known. Images then lead the words, as the daemon types them, so the apps
// show them as pictures.
func (p *ClaudeParser) tidyPrompt(promptID string, msgs []protocol.Message) []protocol.Message {
	marked := false
	for i := range msgs {
		if msgs[i].Role == protocol.RoleUser {
			msgs[i].Text = unwrapPastes(msgs[i].Text)
			marked = marked || claudeImageMark.MatchString(msgs[i].Text)
		}
	}
	if marked && promptID != "" {
		p.prompts.set(promptID, slices.Clone(msgs))
		if sources, ok := p.sources.get(promptID); ok {
			return withImageSources(msgs, sources)
		}
	}
	return withImageSources(msgs, nil)
}

// imageSources reads the record naming the files behind a prompt's
// "[Image #N]" marks. Reading forwards the prompt came first, and is emitted
// again under the same ids with its pictures in place; reading backwards the
// files are kept for the prompt still ahead.
func (p *ClaudeParser) imageSources(rec claudeRecord) []protocol.Message {
	if rec.Type != "user" || rec.PromptID == "" || rec.Message == nil {
		return nil
	}
	var found []string
	for _, m := range claudeImageSource.FindAllStringSubmatch(flattenClaudeResult(rec.Message.Content), -1) {
		// A path with spaces would not survive as one word ahead of the
		// text; its mark stays, and keeps the files after it in step.
		path := strings.TrimSpace(m[1])
		if !filepath.IsAbs(path) || strings.ContainsAny(path, " \t\r\n") {
			path = ""
		}
		found = append(found, path)
	}
	if len(found) == 0 {
		return nil
	}
	prompt, seen := p.prompts.get(rec.PromptID)
	known, _ := p.sources.get(rec.PromptID)
	if seen {
		known = append(slices.Clone(known), found...)
	} else {
		known = append(found, known...)
	}
	p.sources.set(rec.PromptID, known)
	if !seen {
		return nil
	}
	return withImageSources(slices.Clone(prompt), known)
}

// withImageSources puts sources in place of the user's "[Image #N]" marks,
// in order, and brings the images a prompt starts with to its front.
func withImageSources(msgs []protocol.Message, sources []string) []protocol.Message {
	used := 0
	for i := range msgs {
		if msgs[i].Role != protocol.RoleUser {
			continue
		}
		msgs[i].Text = claudeImageMark.ReplaceAllStringFunc(msgs[i].Text, func(mark string) string {
			if used >= len(sources) {
				return mark
			}
			used++
			if sources[used-1] == "" {
				return mark
			}
			return sources[used-1]
		})
		msgs[i].Text = leadingImagesFirst(msgs[i].Text)
	}
	return msgs
}

func unwrapPastes(text string) string {
	if !strings.Contains(text, "pasted_content") {
		return text
	}
	return strings.TrimSpace(claudePasteClose.ReplaceAllString(claudePasteOpen.ReplaceAllString(text, ""), ""))
}

// leadingImagesFirst puts the image paths a prompt starts with on one line,
// a space apart, ahead of its words: the form the apps read pictures from,
// whatever whitespace a paste left between them.
func leadingImagesFirst(text string) string {
	var paths []string
	rest := text
	for {
		trimmed := strings.TrimLeft(rest, " \t\r\n")
		end := strings.IndexAny(trimmed, " \t\r\n")
		if end < 0 {
			end = len(trimmed)
		}
		if !isImagePath(trimmed[:end]) {
			break
		}
		paths = append(paths, trimmed[:end])
		rest = trimmed[end:]
	}
	if len(paths) == 0 {
		return text
	}
	words := strings.TrimSpace(rest)
	if words == "" {
		return strings.Join(paths, " ")
	}
	return strings.Join(paths, " ") + " " + words
}

func isImagePath(token string) bool {
	if !filepath.IsAbs(token) {
		return false
	}
	switch strings.ToLower(filepath.Ext(token)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// claudeNoticeLevels are the levels at which Claude Code shows a system
// record to the person: a compacted conversation, a usage limit, a model
// switched after a refusal. The rest is bookkeeping: turn timings, hook
// summaries ("suggestion"), retries of a failing request, whose final error
// arrives as an assistant message anyway.
var claudeNoticeLevels = map[string]bool{"info": true, "notice": true, "warning": true}

// notice turns a system record the CLI showed into a system row.
func (p *ClaudeParser) notice(rec claudeRecord, offset int64) []protocol.Message {
	var text string
	if !claudeNoticeLevels[rec.Level] || json.Unmarshal(rec.Content, &text) != nil {
		return nil
	}
	text = strings.TrimSpace(text)
	id := rec.UUID
	if id == "" {
		id = fmt.Sprintf("o%d", offset)
	}
	// Newer Claude Codes write a command the person ran here, rather than
	// as their own message.
	if rec.Subtype == "local_command" && !rec.IsMeta {
		rows, _ := p.commandRows(text, id, parseTime(rec.Timestamp), rec.IsSidechain)
		return rows
	}
	if text == "" || rec.IsMeta || claudeCommandWrapper.MatchString(text) {
		return nil
	}
	return []protocol.Message{{
		ID: id, SessionID: p.sessionID, Role: protocol.RoleSystem, Ts: parseTime(rec.Timestamp),
		Text: ClipBlock(text, PreviewLines, PreviewChars), IsSidechain: rec.IsSidechain,
	}}
}

// takenMidTurn turns a message the user sent while Claude was working, and
// which Claude read in the middle of that turn, into a user row.
//
// Claude Code records such a message only as a "queued_command" attachment,
// never as an ordinary user record, so without this it never appeared in the
// conversation at all. Messages read when a turn ends are ordinary user
// records and need nothing here. The same attachment also carries Claude
// Code's own traffic, which nobody typed: reports handed back by its agents
// (origin "peer", marked meta) and background task notifications (a
// different commandMode).
func (p *ClaudeParser) takenMidTurn(rec claudeRecord, offset int64) []protocol.Message {
	a := rec.Attachment
	if a == nil || a.Type != "queued_command" || a.CommandMode != "prompt" || a.IsMeta || rec.IsMeta {
		return nil
	}
	if a.Origin.Kind != "" && a.Origin.Kind != "human" {
		return nil
	}
	text := strings.TrimSpace(flattenClaudeResult(a.Prompt))
	if text == "" || claudeCommandWrapper.MatchString(text) {
		return nil
	}
	text = leadingImagesFirst(unwrapPastes(text))
	id := rec.UUID
	if id == "" {
		id = fmt.Sprintf("o%d", offset)
	}
	return []protocol.Message{{
		ID: id, SessionID: p.sessionID, Role: protocol.RoleUser, Ts: parseTime(rec.Timestamp),
		Text: text, IsSidechain: rec.IsSidechain,
	}}
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
// toolDescription is what the agent said a call is for: Claude Code gives
// every command a description, and a task one too.
func toolDescription(raw json.RawMessage) string {
	var input struct {
		Description string `json:"description"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &input) != nil {
		return ""
	}
	return strings.TrimSpace(input.Description)
}

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
		// A command keeps its newlines: a heredoc'd script flattened to one
		// line is unreadable, and the row only ever shows the first line.
		return ClipBlock(pick("command", "description"), CommandLines, CommandChars)
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
	case "ExitPlanMode":
		// The plan is markdown; its first line, usually a heading, names it.
		return clip(firstLine(pick("plan")), SummaryChars)
	case "AskUserQuestion":
		return clip(claudeQuestionSummary(input), SummaryChars)
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

// firstLine returns the first line with text in it, without a markdown
// heading's marks.
func firstLine(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		if line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#")); line != "" {
			return line
		}
	}
	return ""
}

// claudeQuestionSummary names what AskUserQuestion asked: the first question,
// and how many more came with it.
func claudeQuestionSummary(input map[string]any) string {
	questions, _ := input["questions"].([]any)
	if len(questions) == 0 {
		return ""
	}
	first, _ := questions[0].(map[string]any)
	text, _ := first["question"].(string)
	if strings.TrimSpace(text) == "" {
		text, _ = first["header"].(string)
	}
	text = strings.Join(strings.Fields(text), " ")
	if more := len(questions) - 1; more > 0 && text != "" {
		// The count is kept whole however long the question.
		suffix := fmt.Sprintf(" (+%d more)", more)
		return clip(text, SummaryChars-len(suffix)) + suffix
	}
	return text
}
