package parser

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
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
	// lastStep is the highest step index seen, so the one being written can
	// be named before agy records it. See NextStepIndex.
	lastStep int
	// awaiting is true when the newest record is a prompt, which is the only
	// time a reply is actually being written. See AwaitingResponse.
	awaiting bool
	// open is the steps of calls with no result yet. agy records nothing for
	// a call cancelled at its permission question or interrupted (agy
	// 1.4.8), so the next prompt settles them as interrupted. lastPrompt is
	// the newest prompt's step, for a call met after it paging backwards.
	open       []int
	lastPrompt int
}

type antigravityCall struct {
	// tool is agy's own name for it; name is what the phone is shown.
	tool    string
	name    string
	summary string
	ts      int64
	// written replaces a successful result's text: the new file as a diff,
	// or an artifact's one-line summary.
	written string
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
	// Error is set on a failed step, with the reason on its own.
	Error string `json:"error"`
	// Media is set on a result the model saw as a picture: an image
	// view_file read, copied into the conversation's .tempmediaStorage.
	Media []struct {
		MimeType string `json:"mime_type"`
		URI      string `json:"uri"`
	} `json:"media"`
	ToolCalls []struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"tool_calls"`
}

var antigravityRequest = regexp.MustCompile(`(?s)<USER_REQUEST>\s*(.*?)\s*</USER_REQUEST>`)

func antigravityID(step int) string { return fmt.Sprintf("s%06d", step) }

// NextStepIndex is the index the step now being written will carry.
//
// Antigravity writes a step only once it is finished, so a reply read off the
// pane has no record and no index of its own yet. Predicting it is what lets
// the preview and the finished record share an id, so the row updates in
// place instead of appearing twice.
func (p *AntigravityParser) NextStepIndex() int { return p.lastStep + 1 }

// AwaitingResponse reports whether the model is writing its next response:
// whether the newest record is one the model answers rather than one it wrote.
//
// The transcript answers this; the session's state does not. State comes from
// discovery, which sweeps on its own timer, so for up to a sweep after a
// reply lands it still reads busy. Streaming on that stale answer emitted the
// finished reply a second time, scraped off the pane and wrapped to the
// terminal's width — which is how a table came out as a column of rules.
func (p *AntigravityParser) AwaitingResponse() bool { return p.awaiting }

// antigravityAwaits reports whether a record is answered by a model response.
//
// A prompt is, and so is a system message: agy hands one to the model with the
// next prompt — a subagent's or a background task's news — or on its own when
// a task finishes, and either way a response follows. Only waiting on prompts
// stopped streaming for every turn that began with one, which after a subagent
// or a resume was most of them. A tool's result is answered too, which is the
// reply after a command or an edit. A declined call is not: agy ends the turn
// there.
func antigravityAwaits(step antigravityStep) bool {
	switch step.Type {
	case "USER_INPUT", "SYSTEM_MESSAGE":
		return true
	case "GENERIC":
		return !strings.Contains(step.Error, "user denied permission for ")
	}
	return false
}

// Parse implements Parser.
func (p *AntigravityParser) Parse(line string, offset int64) []protocol.Message {
	var step antigravityStep
	if !decode(line, &step) {
		return nil
	}
	ts := parseTime(step.CreatedAt)
	if step.StepIndex >= p.lastStep {
		p.lastStep = step.StepIndex
		p.awaiting = antigravityAwaits(step)
	}

	switch step.Type {
	case "USER_INPUT":
		out := p.interrupted(step.StepIndex)
		text, images := AntigravityPrompt(step.Content)
		if text == "" && len(images) == 0 {
			return out
		}
		return append(out, protocol.Message{
			ID: antigravityID(step.StepIndex), SessionID: p.sessionID,
			Role: protocol.RoleUser, Ts: ts, Text: text, Images: images,
		})

	case "SYSTEM_MESSAGE":
		text := antigravityNotice(step.Content)
		if text == "" {
			return nil
		}
		return []protocol.Message{{
			ID: antigravityID(step.StepIndex), SessionID: p.sessionID,
			Role: protocol.RoleSystem, Ts: ts, Text: text,
		}}

	case "CHECKPOINT":
		// agy compacted the conversation: what follows starts from a summary.
		if !strings.Contains(step.Content, "Resuming from a compaction") {
			return nil
		}
		return []protocol.Message{{
			ID: antigravityID(step.StepIndex), SessionID: p.sessionID,
			Role: protocol.RoleSystem, Ts: ts, Text: "Context compacted",
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
			args := decodeAntigravityArgs(call.Args)
			remembered := antigravityCall{
				tool:    call.Name,
				name:    antigravityToolName(call.Name, args),
				summary: summarizeAntigravityArgs(call.Name, args),
				ts:      ts,
			}
			switch {
			case remembered.name == "Artifact":
				remembered.written = antigravityArtifactSummary(args)
			case call.Name == "write_to_file":
				remembered.written = antigravityWritten(args)
			}
			p.calls.set(id, remembered)
			message := protocol.Message{
				ID: id, SessionID: p.sessionID, Role: protocol.RoleTool, Ts: ts,
				Tool: &protocol.Tool{Name: remembered.name, Summary: remembered.summary, Status: protocol.ToolRunning},
			}
			// Paging backwards meets the result first.
			if outcome, ok := p.outcomes.get(id); ok {
				outcome = remembered.settle(outcome)
				message.Tool.Status = outcome.status
				message.Text = outcome.preview
			} else if p.lastPrompt > step.StepIndex {
				message.Tool.Status, message.Text = protocol.ToolError, AntigravityInterrupted
			} else {
				p.open = append(p.open, step.StepIndex+1+j)
			}
			out = append(out, message)
		}
		return out

	case "GENERIC":
		id := antigravityID(step.StepIndex)
		p.open = slices.DeleteFunc(p.open, func(call int) bool { return call == step.StepIndex })
		outcome := antigravityOutcome(step)
		p.outcomes.set(id, outcome)
		call, known := p.calls.get(id)
		if !known {
			// Paging backwards, the call is still ahead; it picks this up. A
			// generic step that answers no call is not something to show.
			return nil
		}
		outcome = call.settle(outcome)
		return []protocol.Message{{
			ID: id, SessionID: p.sessionID, Role: protocol.RoleTool, Ts: call.ts,
			Text: outcome.preview,
			Tool: &protocol.Tool{Name: call.name, Summary: call.summary, Status: outcome.status},
		}}
	}
	return nil
}

// antigravityMessage is a message handed to the conversation as a system
// message: from agy itself (sender=system), from a background task
// (sender=<conversation>/task-N), or from another agent (sender=<its id>).
var antigravityMessage = regexp.MustCompile(`(?s)\[Message\] [^\n]*?\bsender=(\S+)[^\n]*?\bcontent=(.*?)\s*</SYSTEM_MESSAGE>`)

// antigravityNotice is the notice for a system message, or "" for one that
// is not a message.
func antigravityNotice(content string) string {
	m := antigravityMessage.FindStringSubmatch(content)
	if m == nil {
		return ""
	}
	sender, text := m[1], strings.Join(strings.Fields(m[2]), " ")
	switch {
	case sender == "system":
		text = strings.TrimPrefix(text, "[Notice] ")
	case strings.Contains(sender, "/task-"):
		// "Task id "<conversation>/task-80" finished…": the task, by its own name.
		text = antigravityTaskName.ReplaceAllString(text, `"$1"`)
	default:
		text = "From another agent: " + text
	}
	return clip(text, 300)
}

var antigravityTaskName = regexp.MustCompile(`"[0-9a-f-]+/(task-\d+)"`)

// antigravityPicture is a picture sent with a message, at its start: one agy
// keeps in the conversation's .user_uploaded folder, where Agentman puts the
// pictures it sends and agy those pasted at its prompt, or one Agentman
// kept, typed before agy had a conversation to put it in. "@" in front
// mentions it.
var antigravityPicture = regexp.MustCompile(`^@?(/\S*/(?:\.gemini/antigravity-cli/brain/[^/\s]+/\.user_uploaded|\.agentman/images/[^/\s]+)/[^/\s]+\.(?i:png|jpe?g|gif|webp))$`)

// agy writes a file named with @ as @[name], and says in the request's
// metadata where each is:
//
//	@[main.go] is a [File]:
//	/Users/me/code/shop/main.go
var (
	antigravityMentionPath = regexp.MustCompile(`(?m)^@\[([^\]\n]+)\] is a \[File\]:\n(/[^\n]*)$`)
	antigravityMention     = regexp.MustCompile(`@\[([^\]\n]+)\]`)
	antigravityLeadMention = regexp.MustCompile(`^@\[([^\]\n]+)\]`)
)

// AntigravityPrompt is what the person sent, as the apps show it: the
// pictures it starts with, and its words, with the files it names written
// as they were typed: by path when the message starts with them, as
// Agentman sends a file, and by name where the words mention one.
func AntigravityPrompt(content string) (string, []string) {
	text := antigravityMentions(AntigravityRequest(content), content)
	var images []string
	for {
		trimmed := strings.TrimLeft(text, " \t\r\n")
		end := strings.IndexAny(trimmed, " \t\r\n")
		if end < 0 {
			end = len(trimmed)
		}
		m := antigravityPicture.FindStringSubmatch(trimmed[:end])
		if m == nil || strings.Contains(m[1], "..") {
			break
		}
		images = append(images, m[1])
		text = strings.TrimSpace(trimmed[end:])
	}
	return text, images
}

// antigravityMentions writes agy's @[name] mentions back as they were typed.
func antigravityMentions(text, content string) string {
	if !strings.Contains(text, "@[") {
		return text
	}
	paths := map[string]string{}
	for _, m := range antigravityMentionPath.FindAllStringSubmatch(content, -1) {
		paths[m[1]] = strings.TrimSpace(m[2])
	}
	var lead []string
	rest := strings.TrimSpace(text)
	for {
		m := antigravityLeadMention.FindStringSubmatch(rest)
		path := ""
		if m != nil {
			path = paths[m[1]]
		}
		if path == "" || strings.ContainsAny(path, " \t") {
			break
		}
		lead = append(lead, "@"+path)
		rest = strings.TrimSpace(rest[len(m[0]):])
	}
	rest = antigravityMention.ReplaceAllString(rest, "@$1")
	switch {
	case len(lead) == 0:
		return rest
	case rest == "":
		return strings.Join(lead, " ")
	}
	return strings.Join(lead, " ") + "\n\n" + rest
}

// AntigravityInterrupted is what a call agy never answered shows.
const AntigravityInterrupted = "Interrupted"

// HasOpenCalls reports whether a call is still waiting for its result.
func (p *AntigravityParser) HasOpenCalls() bool { return len(p.open) > 0 }

// SettleOpenCalls settles every call still waiting for its result as
// interrupted, for a turn that is over without one: agy has gone idle.
func (p *AntigravityParser) SettleOpenCalls() []protocol.Message { return p.settleOpen(math.MaxInt) }

// interrupted settles the calls still open when a prompt arrives: the turn
// they were in is over, and agy wrote nothing for them.
func (p *AntigravityParser) interrupted(prompt int) []protocol.Message {
	p.lastPrompt = max(p.lastPrompt, prompt)
	return p.settleOpen(prompt)
}

func (p *AntigravityParser) settleOpen(before int) []protocol.Message {
	var out []protocol.Message
	kept := p.open[:0]
	for _, step := range p.open {
		if step > before {
			kept = append(kept, step)
			continue
		}
		id := antigravityID(step)
		if call, ok := p.calls.get(id); ok {
			out = append(out, protocol.Message{
				ID: id, SessionID: p.sessionID, Role: protocol.RoleTool, Ts: call.ts,
				Text: AntigravityInterrupted,
				Tool: &protocol.Tool{Name: call.name, Summary: call.summary, Status: protocol.ToolError},
			})
		}
	}
	p.open = kept
	return out
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
