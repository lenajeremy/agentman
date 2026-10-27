package parser

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
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
	// A function-call result can be encountered before its call during backward
	// pagination, or after it during a live forward tail.
	questionCalls map[string]protocol.Message
	// awaiting holds results whose call this parser has not met yet. A result
	// does not name its function, so every one waits, not only a question's.
	// Reading backwards each call follows its result within a few records,
	// which is what keeps this small.
	awaiting map[string]struct{}
}

// NewCodexParser creates a parser bound to one session.
func NewCodexParser(sessionID string) *CodexParser {
	return &CodexParser{sessionID: sessionID,
		questionCalls: map[string]protocol.Message{}, awaiting: map[string]struct{}{}}
}

// AwaitingCalls reports whether a result has been met whose call has not.
// Paging uses it to read on until those calls are reached, so a question and
// its answer settle on the same page. See jsonl.BackwardOptions.Unsettled.
func (p *CodexParser) AwaitingCalls() bool { return len(p.awaiting) > 0 }

type codexRecord struct {
	Timestamp string        `json:"timestamp"`
	Type      string        `json:"type"`
	Payload   *codexPayload `json:"payload"`
}

type codexPayload struct {
	Type      string     `json:"type"`
	Item      *codexItem `json:"item"`
	Name      string     `json:"name"`
	CallID    string     `json:"call_id"`
	Arguments string     `json:"arguments"`

	// turn_aborted
	TurnID string `json:"turn_id"`
	Reason string `json:"reason"`

	// thread_goal_updated
	Goal *struct {
		Objective string `json:"objective"`
		Status    string `json:"status"`
	} `json:"goal"`
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
	Stderr  string                     `json:"stderr"`

	// McpToolCall. Kept raw: their shapes are the server's, and one that
	// did not decode would lose the whole row.
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Result    json.RawMessage `json:"result"`
	Error     json.RawMessage `json:"error"`

	// Extension and ImageView
	Kind      string          `json:"kind"`
	Query     string          `json:"query"`
	SavedPath string          `json:"savedPath"`
	Path      string          `json:"path"`
	Failure   json.RawMessage `json:"failure"`
}

type codexContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
	// Path is a local_image's file.
	Path string `json:"path"`
	// TextElements mark what the composer put in Text for something else:
	// "[Image #1]" where a picture was pasted.
	TextElements []codexTextElement `json:"text_elements"`
}

type codexTextElement struct {
	ByteRange struct {
		Start int `json:"start"`
		End   int `json:"end"`
	} `json:"byte_range"`
	Placeholder string `json:"placeholder"`
}

// codexChange is one file of a FileChange: an update as its hunks, a new or
// deleted file as its whole text.
type codexChange struct {
	Type        string `json:"type"`
	UnifiedDiff string `json:"unified_diff"`
	Content     string `json:"content"`
	MovePath    string `json:"move_path"`
}

type codexParsedCmd struct {
	Type string `json:"type"`
	Cmd  string `json:"cmd"`
}

// CodexUserText is the text of the user message a rollout line records, or
// "" for any other line, with each pasted picture's path where its mark is:
// the paths and the words, as the daemon sends a message with pictures. A
// message Codex reads in the middle of a turn is written only when it reads
// it, so this is how the daemon tells that a message it sent has arrived.
func CodexUserText(line string) string {
	var rec codexRecord
	if !decode(line, &rec) || rec.Type != "event_msg" || rec.Payload == nil ||
		rec.Payload.Type != "item_completed" || rec.Payload.Item == nil ||
		rec.Payload.Item.Type != "UserMessage" {
		return ""
	}
	return codexMarkedText(rec.Payload.Item.Content, func(path string) string { return path })
}

// Parse implements Parser.
func (p *CodexParser) Parse(line string, offset int64) []protocol.Message {
	var rec codexRecord
	if !decode(line, &rec) {
		return nil
	}
	if rec.Type == "response_item" && rec.Payload != nil {
		return p.parseQuestionCall(rec)
	}
	if rec.Type != "event_msg" || rec.Payload == nil {
		return nil
	}
	if rec.Payload.Type == "turn_aborted" {
		return p.interrupted(rec, offset)
	}
	if rec.Payload.Type == "thread_goal_updated" {
		return p.goal(rec, offset)
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
		body, images := codexUserMessage(item.Content)
		if body == "" && len(images) == 0 {
			return nil
		}
		if notice := codexAutomation(body); notice != "" {
			base.Role = protocol.RoleSystem
			base.Text = notice
			return []protocol.Message{base}
		}
		// /init sends the model a page of instructions in the person's
		// name; what they did was run the command.
		if strings.HasPrefix(body, codexInitPrompt) {
			body = "/init"
		}
		base.Role = protocol.RoleUser
		base.Text = body
		base.Images = images
		return []protocol.Message{base}

	case "AgentMessage":
		body := codexCitations(collectCodexText(item.Content))
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
		base.Text = ClipBlock(output, PreviewLines, PreviewChars)
		base.Tool = &protocol.Tool{
			Name:    "Shell",
			Summary: ClipBlock(command, CommandLines, CommandChars),
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
		status := protocol.ToolOK
		if item.Status == "failed" || item.Status == "declined" {
			status = protocol.ToolError
		}
		base.Tool = &protocol.Tool{Name: "Edit", Summary: clip(summary, SummaryChars), Status: status}
		// What went wrong when it did; otherwise the change itself.
		if status == protocol.ToolError && strings.TrimSpace(item.Stderr) != "" {
			base.Text = ClipBlock(item.Stderr, PreviewLines, PreviewChars)
		} else if diff := codexDiffs(paths, item.Changes); diff != "" {
			base.Text = ClipBlock(diff, PreviewLines, PreviewChars)
		}
		return []protocol.Message{base}

	case "McpToolCall":
		server, tool := item.Server, item.Tool
		if server == "" {
			server = "mcp"
		}
		if tool == "" {
			tool = "call"
		}
		name := server + "." + tool
		status := protocol.ToolOK
		if item.Status == "failed" {
			status = protocol.ToolError
		}
		args, own := codexArguments(item.Arguments)
		summary := summarizeToolInput(name, args)
		title := ""
		if own != "" {
			title = DescribeTool(name, summary, own)
		}
		output, failed := codexToolOutcome(item.Result, item.Error)
		if failed {
			status = protocol.ToolError
		}
		base.Role = protocol.RoleTool
		base.Text = output
		base.Tool = &protocol.Tool{Name: name, Summary: summary, Status: status, Title: title}
		return []protocol.Message{base}

	case "ContextCompaction":
		base.Role = protocol.RoleSystem
		base.Text = "Context compacted"
		return []protocol.Message{base}

	case "WebSearch":
		base.Role = protocol.RoleTool
		base.Tool = &protocol.Tool{Name: "Web search", Summary: clip(item.Query, SummaryChars), Status: protocol.ToolOK}
		return []protocol.Message{base}

	case "Extension":
		base.Role = protocol.RoleTool
		tool := &protocol.Tool{Status: protocol.ToolOK}
		switch item.Kind {
		case "web.search":
			tool.Name, tool.Summary = "Web search", clip(item.Query, SummaryChars)
		case "clock.sleep":
			tool.Name = "Wait"
		case "image_gen.generation":
			tool.Name, tool.Summary = "Image generation", clip(item.SavedPath, SummaryChars)
			if len(item.Failure) > 0 && string(item.Failure) != "null" {
				tool.Status = protocol.ToolError
			}
		default:
			tool.Name, tool.Summary = "Extension", clip(item.Kind, SummaryChars)
		}
		base.Tool = tool
		return []protocol.Message{base}

	case "ImageView":
		// "[image]" is what every parser leaves where a picture was read, and
		// the path as a plain absolute path is what lets the app offer it:
		// Codex records a file:// URL, which neither the daemon nor the app
		// treats as a file the agent opened. Not clipped either — half a path
		// opens nothing. Named a read, as Claude's are, because that is what
		// the apps show a picture under and line several up side by side by.
		base.Role = protocol.RoleTool
		base.Text = "[image]"
		base.Tool = &protocol.Tool{Name: "Read", Summary: codexFilePath(item.Path), Status: protocol.ToolOK}
		return []protocol.Message{base}

	case "CollabAgentToolCall":
		status := protocol.ToolOK
		if item.Status == "failed" {
			status = protocol.ToolError
		}
		base.Role = protocol.RoleTool
		base.Tool = &protocol.Tool{Name: "Agent", Summary: clip(item.Tool, SummaryChars), Status: status}
		return []protocol.Message{base}

	case "SubAgentActivity":
		base.Role = protocol.RoleSystem
		base.Text = "Subagent: " + clip(item.Kind, SummaryChars)
		return []protocol.Message{base}

	case "EnteredReviewMode":
		base.Role = protocol.RoleSystem
		base.Text = "Review started"
		return []protocol.Message{base}

	case "ExitedReviewMode":
		base.Role = protocol.RoleSystem
		base.Text = "Review finished"
		return []protocol.Message{base}

	default:
		// "Reasoning" is dropped for the same reason as Claude's "thinking"
		// blocks: long, largely opaque, and not what a phone glance is for.
		return nil
	}
}

func (p *CodexParser) parseQuestionCall(rec codexRecord) []protocol.Message {
	call := rec.Payload
	if call.CallID == "" {
		return nil
	}
	switch call.Type {
	case "function_call":
		_, answered := p.awaiting[call.CallID]
		delete(p.awaiting, call.CallID)
		if call.Name != "request_user_input" && call.Name != "request_user_input_async" {
			return nil
		}
		var args struct {
			Questions []struct {
				Title    string `json:"title"`
				Question string `json:"question"`
			} `json:"questions"`
		}
		_ = json.Unmarshal([]byte(call.Arguments), &args)
		summary := "Asked for input"
		if len(args.Questions) == 1 {
			summary = args.Questions[0].Title
			if summary == "" {
				summary = args.Questions[0].Question
			}
		} else if len(args.Questions) > 1 {
			summary = fmt.Sprintf("Asked %d questions", len(args.Questions))
		}
		status := protocol.ToolRunning
		if answered {
			status = protocol.ToolOK
		}
		message := protocol.Message{
			ID: "codex-question:" + call.CallID, SessionID: p.sessionID,
			Role: protocol.RoleTool, Ts: parseTime(rec.Timestamp),
			Tool: &protocol.Tool{Name: "Question", Summary: clip(summary, SummaryChars), Status: status},
		}
		if status == protocol.ToolRunning {
			p.questionCalls[call.CallID] = message
		}
		return []protocol.Message{message}
	case "function_call_output":
		if message, ok := p.questionCalls[call.CallID]; ok {
			delete(p.questionCalls, call.CallID)
			settled := *message.Tool
			settled.Status = protocol.ToolOK
			message.Tool = &settled
			return []protocol.Message{message}
		}
		// A live tail meets results of calls written before it started, and
		// those never clear, hence the bound.
		if len(p.awaiting) < maxAwaitingCalls {
			p.awaiting[call.CallID] = struct{}{}
		}
	}
	return nil
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

// codexImageMark is the composer's mark for the Nth picture of a message.
var codexImageMark = regexp.MustCompile(`^\[Image #(\d+)\]$`)

// codexUserMessage is what the person sent, as the apps show it: the
// pictures that went with it, and its words without the marks the composer
// left where each was pasted. A picture Codex kept only as data, with no
// file to show, keeps its mark.
func codexUserMessage(content []codexContent) (string, []string) {
	pictures := codexPictures(content)
	var images []string
	for _, path := range pictures {
		if path != "" {
			images = append(images, path)
		}
	}
	text := codexMarkedText(content, func(string) string { return "" })
	text = codexAppRequest(text, images)
	text = codexSpoken(text)
	text = codexAnswers(text)
	return text, images
}

// codexPictures lists a message's pictures in the order its marks count
// them: the file of each, or "" for one Codex kept only as data.
func codexPictures(content []codexContent) []string {
	var pictures []string
	for _, block := range content {
		switch strings.ToLower(block.Type) {
		case "local_image":
			if filepath.IsAbs(block.Path) && filepath.Clean(block.Path) == block.Path {
				pictures = append(pictures, block.Path)
			} else {
				pictures = append(pictures, "")
			}
		case "image", "input_image":
			pictures = append(pictures, "")
		}
	}
	return pictures
}

// codexMarkedText is a message's words with the mark of each picture that
// has a file put to with(path).
func codexMarkedText(content []codexContent, with func(path string) string) string {
	pictures := codexPictures(content)
	var parts []string
	for _, block := range content {
		switch strings.ToLower(block.Type) {
		case "text", "input_text", "output_text":
			if text := replaceImageMarks(block.Text, block.TextElements, pictures, with); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

// replaceImageMarks puts with(path) in place of the mark of each picture
// that has a file. A mark taken out takes a space beside it, so no gap is
// left.
func replaceImageMarks(text string, elements []codexTextElement, pictures []string, with func(path string) string) string {
	marks := slices.Clone(elements)
	// Last first, so the ranges still to come keep pointing where they did.
	slices.SortFunc(marks, func(a, b codexTextElement) int { return b.ByteRange.Start - a.ByteRange.Start })
	for _, mark := range marks {
		start, end := mark.ByteRange.Start, mark.ByteRange.End
		m := codexImageMark.FindStringSubmatch(mark.Placeholder)
		if m == nil || start < 0 || end > len(text) || start >= end || text[start:end] != mark.Placeholder {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 || n > len(pictures) || pictures[n-1] == "" {
			continue
		}
		replacement := with(pictures[n-1])
		if replacement == "" {
			if end < len(text) && text[end] == ' ' {
				end++
			} else if start > 0 && text[start-1] == ' ' {
				start--
			}
		}
		text = text[:start] + replacement + text[end:]
	}
	return text
}

// The Codex app sends a message with more than the person's words as a
// document of its own, ending in a heading over what they asked. Ahead of it
// can be the state of its browser, and a heading per file attached or text
// pasted as one, naming where it is.
var (
	codexBrowserState   = regexp.MustCompile(`(?s)^<in-app-browser-context[^>]*>.*?</in-app-browser-context>`)
	codexFilesHeading   = regexp.MustCompile(`^# Files (?:mentioned|pasted) by the user:`)
	codexFileHeading    = regexp.MustCompile(`(?m)^## [^\n]*?: (/[^\n]*)$`)
	codexRequestHeading = regexp.MustCompile(`(?m)^## My request(?: for Codex)?:[ \t]*\n?`)
)

// codexAppRequest turns that document back into the message: the files that
// are not pictures named first as @path, as Agentman sends files, then the
// request. The pictures travel with the message already.
func codexAppRequest(text string, images []string) string {
	if browser := codexBrowserState.FindStringIndex(text); browser != nil {
		rest := strings.TrimSpace(text[browser[1]:])
		if at := codexRequestHeading.FindStringIndex(rest); at != nil && at[0] == 0 {
			return strings.TrimSpace(rest[at[1]:])
		}
		text = rest
	}
	if !codexFilesHeading.MatchString(text) {
		return text
	}
	at := codexRequestHeading.FindStringIndex(text)
	if at == nil {
		return text
	}
	var refs []string
	for _, m := range codexFileHeading.FindAllStringSubmatch(text[:at[0]], -1) {
		if path := strings.TrimSpace(m[1]); !slices.Contains(images, path) {
			refs = append(refs, "@"+path)
		}
	}
	request := strings.TrimSpace(text[at[1]:])
	switch {
	case len(refs) == 0:
		return request
	case request == "":
		return strings.Join(refs, " ")
	}
	return strings.Join(refs, " ") + "\n\n" + request
}

// An answer given in the Codex app to a question Codex asked reaches it as
// the question and answer in JSON. The question has its own row already.
var codexAnswerReply = regexp.MustCompile(`(?s)^<send_user_message_question_reply>(.*)</send_user_message_question_reply>$`)

// codexAnswers is what the person answered, one answer a line.
func codexAnswers(text string) string {
	reply := codexAnswerReply.FindStringSubmatch(text)
	if reply == nil {
		return text
	}
	var answers []struct {
		Answer string `json:"answer"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(reply[1])), &answers) != nil {
		return text
	}
	var said []string
	for _, answer := range answers {
		if a := strings.TrimSpace(answer.Answer); a != "" {
			said = append(said, a)
		}
	}
	return strings.Join(said, "\n")
}

// A scheduled automation starts its run with a message of its own, its
// instructions in full, which nobody typed.
var (
	codexAutomationRun = regexp.MustCompile(`^<heartbeat>`)
	codexAutomationID  = regexp.MustCompile(`<automation_id>([^<\n]*)</automation_id>`)
)

// codexAutomation is the notice for an automation's run, or "" when text is
// not one.
func codexAutomation(text string) string {
	if !codexAutomationRun.MatchString(text) {
		return ""
	}
	if m := codexAutomationID.FindStringSubmatch(text); m != nil && strings.TrimSpace(m[1]) != "" {
		return "Automation " + strings.TrimSpace(m[1]) + " started a run"
	}
	return "An automation started a run"
}

// A turn spoken in a voice session reaches the agent wrapped, with the
// conversation so far. What the person said is its input. The handoff when
// the session ends is Codex's own note, not anything they said.
var (
	codexVoiceTurn  = regexp.MustCompile(`(?s)^<realtime_delegation>(.*)</realtime_delegation>$`)
	codexVoiceInput = regexp.MustCompile(`(?s)<input>(.*?)</input>`)
	codexVoiceEnded = regexp.MustCompile(`<source>\s*transcript_tail_flush\s*</source>`)
)

// codexSpoken is what the person said, for a turn spoken in a voice session.
func codexSpoken(text string) string {
	turn := codexVoiceTurn.FindStringSubmatch(text)
	if turn == nil {
		return text
	}
	if codexVoiceEnded.MatchString(turn[1]) {
		return ""
	}
	if input := codexVoiceInput.FindStringSubmatch(turn[1]); input != nil {
		return strings.TrimSpace(input[1])
	}
	return text
}

// The Codex app names a file it made in a reply as a directive it draws as
// a chip; elsewhere it reads as its path.
var (
	codexFileCitation = regexp.MustCompile(`:codex-file-citation\{[^}\n]*\}`)
	codexCitedPath    = regexp.MustCompile(`path="([^"]*)"`)
)

func codexCitations(text string) string {
	if !strings.Contains(text, ":codex-file-citation{") {
		return text
	}
	return strings.TrimSpace(codexFileCitation.ReplaceAllStringFunc(text, func(citation string) string {
		if m := codexCitedPath.FindStringSubmatch(citation); m != nil && m[1] != "" {
			return "`" + m[1] + "`"
		}
		return ""
	}))
}

// codexDiffs renders a FileChange's files, in the order given, as one
// unified diff, which the apps colour.
func codexDiffs(paths []string, changes map[string]json.RawMessage) string {
	var diffs []string
	for _, path := range paths {
		var change codexChange
		if json.Unmarshal(changes[path], &change) != nil {
			continue
		}
		switch change.Type {
		case "add":
			if diff := UnifiedDiff(path, "", change.Content); diff != "" {
				diffs = append(diffs, diff)
			}
		case "delete":
			if diff := UnifiedDiff(path, change.Content, ""); diff != "" {
				diffs = append(diffs, diff)
			}
		case "update":
			hunks := strings.Trim(change.UnifiedDiff, "\n")
			if hunks == "" {
				continue
			}
			if !strings.HasPrefix(hunks, "--- ") {
				to := path
				if change.MovePath != "" {
					to = change.MovePath
				}
				hunks = fmt.Sprintf("--- a/%s\n+++ b/%s\n%s",
					strings.TrimPrefix(path, "/"), strings.TrimPrefix(to, "/"), hunks)
			}
			diffs = append(diffs, hunks)
		}
	}
	return strings.Join(diffs, "\n")
}

// codexArguments splits an MCP call's arguments into what it was given and
// the agent's own words for the step, which Codex asks for as its title.
func codexArguments(raw json.RawMessage) (json.RawMessage, string) {
	var args map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &args) != nil {
		return nil, ""
	}
	own, _ := args["title"].(string)
	if own == "" {
		return raw, ""
	}
	delete(args, "title")
	rest, err := json.Marshal(args)
	if err != nil {
		return raw, own
	}
	return rest, own
}

// codexToolOutcome is what an MCP call returned, or why it failed, and
// whether it did.
func codexToolOutcome(result, failure json.RawMessage) (string, bool) {
	var reason struct {
		Message string `json:"message"`
	}
	if len(failure) > 0 && string(failure) != "null" {
		var plain string
		if json.Unmarshal(failure, &plain) == nil && strings.TrimSpace(plain) != "" {
			return ClipBlock(plain, PreviewLines, PreviewChars), true
		}
		if json.Unmarshal(failure, &reason) == nil && strings.TrimSpace(reason.Message) != "" {
			return ClipBlock(reason.Message, PreviewLines, PreviewChars), true
		}
	}
	var returned struct {
		Content []codexContent `json:"content"`
		IsError bool           `json:"isError"`
	}
	if len(result) == 0 || json.Unmarshal(result, &returned) != nil {
		return "", false
	}
	var parts []string
	for _, block := range returned.Content {
		switch {
		case block.Text != "":
			parts = append(parts, block.Text)
		case block.Type == "image":
			parts = append(parts, "[image]")
		}
	}
	return ClipBlock(strings.Join(parts, "\n"), PreviewLines, PreviewChars), returned.IsError
}

// codexInitPrompt opens what Codex sends the model for /init.
const codexInitPrompt = "Generate a file named AGENTS.md that serves as a contributor guide for this repository."

// goal is the notice for a goal set with /goal, or one whose state changed.
func (p *CodexParser) goal(rec codexRecord, offset int64) []protocol.Message {
	goal := rec.Payload.Goal
	if goal == nil || strings.TrimSpace(goal.Objective) == "" {
		return nil
	}
	text := "Goal: "
	if status := strings.ReplaceAll(strings.TrimSpace(goal.Status), "_", " "); status != "" && status != "active" {
		text = "Goal " + status + ": "
	}
	return []protocol.Message{{
		ID: fmt.Sprintf("o%d", offset), SessionID: p.sessionID, Role: protocol.RoleSystem,
		Ts: parseTime(rec.Timestamp), Text: text + clip(strings.Join(strings.Fields(goal.Objective), " "), 300),
	}}
}

// interrupted is the notice for a turn the person stopped, from the
// terminal or from Agentman. Codex ends turns early for its own reasons too,
// which say nothing to them.
func (p *CodexParser) interrupted(rec codexRecord, offset int64) []protocol.Message {
	if rec.Payload.Reason != "interrupted" {
		return nil
	}
	id := "codex-interrupted:" + rec.Payload.TurnID
	if rec.Payload.TurnID == "" {
		id = fmt.Sprintf("o%d", offset)
	}
	return []protocol.Message{{
		ID: id, SessionID: p.sessionID, Role: protocol.RoleSystem,
		Ts: parseTime(rec.Timestamp), Text: "Interrupted",
	}}
}

func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// codexFilePath turns the file:// URL Codex records for an image it viewed
// into the absolute path it names, decoding escapes such as the %20 in a
// macOS screenshot's name. Anything else is returned as it came.
func codexFilePath(raw string) string {
	if !strings.HasPrefix(raw, "file://") {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Path == "" || (parsed.Host != "" && parsed.Host != "localhost") {
		return raw
	}
	return parsed.Path
}
