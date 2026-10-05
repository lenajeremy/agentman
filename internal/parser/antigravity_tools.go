package parser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// What Antigravity's tools are called on the phone, what one line of their
// input is worth showing, and what is left of their output once agy's notes to
// the model are taken out of it.
//
// A tool result in agy's transcript is the text the model was given, so much
// of it is addressed to the model rather than to a reader: "If relevant,
// proactively run terminal commands…", "The following code has been modified
// to include a line number before every line…", "Do not attempt to circumvent
// this denial…". On a phone those lines are most of what a row showed. Each
// shape below is copied from agy 1.2.17.

// antigravityToolName gives Antigravity's tools the names the app knows how
// to render: a "Shell" row shows its command, a "Read" row can open an image,
// an "Edit" row colours a diff.
func antigravityToolName(name string, args map[string]any) string {
	switch {
	case name == "run_command":
		return "Shell"
	case name == "command_status":
		return "Command status"
	case name == "send_command_input":
		return "Command input"
	case name == "read_terminal":
		return "Terminal"
	case name == "view_file", name == "view_file_outline", name == "view_code_item", name == "view_content_chunk":
		return "Read"
	case name == "write_to_file":
		// A plan, task list or walkthrough is written with this same tool, and
		// says so by carrying ArtifactMetadata.
		if _, artifact := args["ArtifactMetadata"].(map[string]any); artifact {
			return "Artifact"
		}
		return "Write"
	case name == "replace_file_content", name == "multi_replace_file_content":
		return "Edit"
	case name == "list_dir":
		return "List"
	case name == "grep_search":
		return "Grep"
	case name == "find_by_name":
		return "Find"
	case name == "codebase_search":
		return "Search"
	case name == "search_web":
		return "Web search"
	case name == "read_url_content":
		return "Web fetch"
	case name == "ask_question":
		return "Question"
	case name == "invoke_subagent":
		return "Agent"
	case name == "manage_task":
		return "Task"
	case name == "generate_image":
		return "Image"
	case name == "browser_subagent", name == "open_browser_url", name == "read_browser_page",
		name == "capture_browser_screenshot", strings.HasPrefix(name, "browser_"):
		return "Browser"
	case name == "":
		return "Tool"
	}
	return name
}

// summarizeAntigravityArgs picks the part of a call worth a glance. Argument
// names are Antigravity's own — CommandLine, AbsolutePath, TargetFile — and,
// for a few tools, lower case: search_web takes "query", ask_question
// "questions".
//
// Every call also carries toolSummary, agy's own few-word caption ("View
// shot.png"), which is the fallback for a tool not handled here: readable,
// if less specific than the argument itself.
func summarizeAntigravityArgs(name string, args map[string]any) string {
	if args == nil {
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
	switch name {
	case "run_command":
		return ClipBlock(pick("CommandLine"), CommandLines, CommandChars)
	case "ask_question":
		if questions, ok := args["questions"].([]any); ok && len(questions) > 0 {
			first, _ := questions[0].(map[string]any)
			text, _ := first["question"].(string)
			if more := len(questions) - 1; more > 0 {
				text += fmt.Sprintf(" (+%d more)", more)
			}
			if strings.TrimSpace(text) != "" {
				return clip(text, SummaryChars)
			}
		}
	case "invoke_subagent":
		if subagents, ok := args["Subagents"].([]any); ok && len(subagents) > 0 {
			first, _ := subagents[0].(map[string]any)
			role, _ := first["Role"].(string)
			prompt, _ := first["Prompt"].(string)
			text := strings.TrimSpace(role)
			if prompt = strings.TrimSpace(prompt); prompt != "" {
				if text != "" {
					text += ": "
				}
				text += prompt
			}
			if text != "" {
				return clip(text, SummaryChars)
			}
		}
	case "manage_task":
		action := pick("Action")
		task := pick("TaskId")
		if slash := strings.LastIndexByte(task, '/'); slash >= 0 {
			task = task[slash+1:] // "<conversation>/task-80" → "task-80"
		}
		if text := strings.TrimSpace(action + " " + task); text != "" {
			return text
		}
	}
	// A path is kept whole. Clipped, it is no longer a path: the phone could
	// not open it, and the daemon would not record it as a file the agent
	// touched.
	if path := pick("AbsolutePath", "TargetFile", "File", "DirectoryPath", "SearchPath",
		"SearchDirectory"); path != "" && !strings.ContainsAny(path, "\n") {
		return path
	}
	if value := pick("Url", "query", "Query", "Pattern"); value != "" {
		return clip(value, SummaryChars)
	}
	return clip(pick("toolSummary", "toolAction"), SummaryChars)
}

var (
	antigravityExit = regexp.MustCompile(`^The command exited with code (-?\d+)\.?$`)
	// antigravityNotes are lines agy addresses to the model. Matched whole.
	antigravityNotes = regexp.MustCompile(`^(?:` +
		`Created At: .*|Completed At: .*|` +
		`If relevant, proactively run terminal commands.*|` +
		`Note: You have just created an artifact.*|` +
		`File Path: .*|Total Lines: \d+|Total Bytes: \d+|Showing lines \d+ to \d+|` +
		`The following code has been modified to include a line number.*|` +
		`The above content (?:shows|does NOT show) the entire.*|` +
		`The following is the entire, complete content of the requested file\.|` +
		`Please note that the above snippet only shows the MODIFIED lines.*|` +
		`The search for ".*" returned the following summary:` +
		`)$`)
	antigravityDiffBlock = regexp.MustCompile(`(?s)\[diff_block_start\]\n(.*?)\n?\[diff_block_end\]`)
	antigravityTaskID    = regexp.MustCompile(`task id: (?:[0-9a-f-]+/)?(\S+)`)
)

// antigravityOutcome is what a tool's result step says, ready for the row.
//
// It is worked out from the result alone, because paging backwards meets a
// result before its call. What only the call knows — the contents of a written
// file — is laid over it by settle once both are in hand.
func antigravityOutcome(step antigravityStep) toolOutcome {
	outcome := toolOutcome{status: protocol.ToolOK}
	switch strings.ToUpper(step.Status) {
	case "ERROR", "FAILED", "CANCELED", "CANCELLED":
		outcome.status = protocol.ToolError
		outcome.preview = antigravityError(step)
		return outcome
	case "RUNNING":
		// A command sent to the background. Nothing later in the transcript
		// settles this step — the task reports back as a system message, and
		// manage_task reads its output — so a row left "running" would spin
		// for ever.
		text := "Running in the background."
		if match := antigravityTaskID.FindStringSubmatch(step.Content); match != nil {
			text = "Running in the background as " + match[1] + "."
		}
		outcome.preview = text
		return outcome
	}

	if len(step.Media) > 0 {
		// An image the model looked at. The row's path opens it; the text
		// only has to say what came back, the way Claude's rows do.
		outcome.preview = "[image]"
		return outcome
	}

	// An edit's result carries the change as a real unified diff, hunk header
	// and context lines included, between two markers. Shown bare, it is
	// coloured on the phone like any other diff.
	if blocks := antigravityDiffBlock.FindAllStringSubmatch(step.Content, -1); len(blocks) > 0 {
		diffs := make([]string, 0, len(blocks))
		for _, block := range blocks {
			diffs = append(diffs, strings.TrimRight(block[1], "\n"))
		}
		outcome.preview = ClipBlock(strings.Join(diffs, "\n"), PreviewLines, PreviewChars)
		return outcome
	}

	// invoke_subagent answers with the new conversations' ids and log paths,
	// which are the model's to use and mean nothing on a phone.
	if strings.Contains(step.Content, "Created the following subagents:") {
		count := max(strings.Count(step.Content, `"conversationId"`), 1)
		outcome.preview = fmt.Sprintf("Started %d subagent%s.", count, map[bool]string{true: "s"}[count > 1])
		return outcome
	}

	var kept []string
	for _, line := range strings.Split(step.Content, "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if antigravityNotes.MatchString(trimmed) || trimmed == "Output:" {
			continue
		}
		if match := antigravityExit.FindStringSubmatch(trimmed); match != nil {
			if code, err := strconv.Atoi(match[1]); err == nil && code != 0 {
				outcome.status = protocol.ToolError
				kept = append(kept, fmt.Sprintf("Exited with code %d.", code))
			}
			continue
		}
		kept = append(kept, line)
	}
	outcome.preview = ClipBlock(strings.TrimSpace(strings.Join(kept, "\n")), PreviewLines, PreviewChars)
	return outcome
}

// settle combines a result with the call it answers. A file write's result
// only says the file was created; the call holds what went into it.
func (call antigravityCall) settle(outcome toolOutcome) toolOutcome {
	if outcome.status == protocol.ToolOK && call.written != "" {
		outcome.preview = call.written
	}
	return outcome
}

// antigravityError reduces a failed step to the reason, without agy's framing
// ("Encountered error in step execution: ") or its instructions to the model.
func antigravityError(step antigravityStep) string {
	text := step.Error
	if strings.TrimSpace(text) == "" {
		text = step.Content
		var kept []string
		for _, line := range strings.Split(text, "\n") {
			if !antigravityNotes.MatchString(strings.TrimSpace(line)) {
				kept = append(kept, line)
			}
		}
		text = strings.Join(kept, "\n")
	}
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "Encountered error in step execution: ")
	if cut := strings.Index(text, "\nDo not attempt to circumvent"); cut >= 0 {
		text = text[:cut]
	}
	// A tool call the model got wrong is reported with the whole chain of
	// where it was caught: "declaring permissions: cortex tool view_file:
	// convert tool call for permissions: model output error: invalid tool
	// call error (invalid_args) failed to read file: …". The last part is
	// the reason.
	if cut := strings.LastIndex(text, "(invalid_args) "); cut >= 0 {
		text = text[cut+len("(invalid_args) "):]
	}
	// "permission check failed for write_file "/w/a": user denied permission
	// for write_file(/w/a)" says the same thing twice.
	if cut := strings.Index(text, "user denied permission for "); cut > 0 {
		text = text[cut:]
	}
	return ClipBlock(strings.TrimSpace(text), PreviewLines, PreviewChars)
}

// antigravityWritten renders a file write as the diff of a new file, which is
// what the app colours: the lines the agent wrote, each marked as added.
//
// agy records the content it wrote, not what was there before, so an
// overwrite reads as every line added — still exactly what is in the file now.
func antigravityWritten(args map[string]any) string {
	content, ok := args["CodeContent"].(string)
	if !ok || content == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, line := range lines {
		b.WriteString("+")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return ClipBlock(strings.TrimRight(b.String(), "\n"), PreviewLines, PreviewChars)
}

// antigravityArtifactSummary is the one-line description agy keeps for an
// artifact it writes, shown in place of the document itself.
func antigravityArtifactSummary(args map[string]any) string {
	metadata, _ := args["ArtifactMetadata"].(map[string]any)
	summary, _ := metadata["Summary"].(string)
	return strings.TrimSpace(summary)
}

func decodeAntigravityArgs(raw json.RawMessage) map[string]any {
	var args map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &args) != nil {
		return nil
	}
	return args
}
