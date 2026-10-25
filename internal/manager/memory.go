package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/lenajeremy/agentman/internal/knowledge"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// Agentman's memory: the conversation is kept short, and what lasts goes to
// its private notes.
//
// Compaction: once a request to the model passes compactAt tokens, the
// history before the last turns is summarised and replaced by the summary,
// on either engine. The old history is archived, never lost, and the apps
// show a quiet divider; what they show of the conversation is unchanged.
//
// A new chat: the history and the conversation are archived and start
// again, with the cards still waiting carried over.
//
// Either way, Agentman first keeps any lasting facts and the user's
// preferences in its private notes, which it also writes, updates and
// deletes on its own with its tools. Shared notes, which every agent reads,
// still wait for the user's approval.

const (
	// compactAt is the size, in tokens, at which a conversation is
	// compacted: past the system prompt and tools, room for a long exchange,
	// short of where every message gets expensive.
	compactAt = 60_000
	// keepTurns is how many of the latest turns stay word for word.
	keepTurns = 2
	// maxKeptTail bounds those turns; past it only the last one is kept.
	maxKeptTail = 25_000
	// maxTranscript bounds what a summary is written from, keeping the end.
	maxTranscript = 400_000
	// maxRemembered bounds what one compaction keeps in the notes.
	maxRemembered = 8
)

const compactPrompt = `You are Agentman, an assistant that manages the user's coding agents. ` +
	`Your conversation with the user is about to be shortened: everything in the transcript below will be ` +
	`replaced by your summary, so write what you will need to carry on as if you remembered it.

Return JSON:
- summary: the conversation so far, for yourself. What the user asked and wanted, what you did, what each agent ` +
	`is working on or waiting for, decisions made, and anything still open. Plain, specific, a few short paragraphs ` +
	`at most; names of agents, files and commands kept exact.
- remember: lasting facts and the user's preferences worth keeping beyond this conversation, as private notes: ` +
	`how they like things done, facts about their projects and setup. Not passing state, not what any agent is doing ` +
	`now. Update a note you already keep (give its name) rather than writing a second one. Often there is nothing; ` +
	`then return an empty list.`

var compactSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"summary": map[string]any{"type": "string"},
		"remember": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{"type": "string"},
					"body":  map[string]any{"type": "string"},
					"name":  map[string]any{"type": "string"},
				},
				"required":             []string{"title", "body", "name"},
				"additionalProperties": false,
			},
		},
	},
	"required":             []string{"summary", "remember"},
	"additionalProperties": false,
}

type remembered struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Name  string `json:"name"`
}

// compactIfNeeded compacts the conversation when it has grown past
// compactAt. It reports whether it did.
func (m *Manager) compactIfNeeded(ctx context.Context, model Model) bool {
	m.compactMu.Lock()
	defer m.compactMu.Unlock()
	if m.historyTokens() < compactAt {
		return false
	}
	return m.compactLocked(ctx, model) == nil
}

// historyTokens is how large the conversation is, in tokens: what the last
// request used, or an estimate from the history after a restart.
func (m *Manager) historyTokens() int {
	m.mu.Lock()
	known := m.promptTokens
	m.mu.Unlock()
	if known > 0 {
		return known
	}
	return estimateTokens(m.conversation.messages())
}

// notePromptTokens records how large a request was, from its usage.
func (m *Manager) notePromptTokens(reply *anthropic.BetaMessage) {
	if reply == nil {
		return
	}
	usage := reply.Usage
	total := int(usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens)
	m.mu.Lock()
	m.promptTokens = total
	m.mu.Unlock()
}

var imageData = regexp.MustCompile(`"data":"[A-Za-z0-9+/=]{1000,}"`)

// estimateTokens guesses a history's size: about four characters a token,
// and an image counted as what a model charges for one rather than as its
// base64.
func estimateTokens(history []anthropic.BetaMessageParam) int {
	total := 0
	for _, message := range history {
		raw, err := json.Marshal(message)
		if err != nil {
			continue
		}
		images := len(imageData.FindAll(raw, -1))
		text := imageData.ReplaceAll(raw, nil)
		total += len(text)/4 + images*1600
	}
	return total
}

// compactLocked summarises the history before the latest turns and puts the
// summary in its place. m.compactMu is held.
func (m *Manager) compactLocked(ctx context.Context, model Model) error {
	history := m.conversation.messages()
	cut := tailStart(history)
	if cut <= 0 {
		return nil
	}
	result, err := m.summarise(ctx, model, history[:cut], true)
	if err != nil {
		m.addEntry(protocol.ManagerEntry{Role: "error", Text: "Couldn't compact the conversation: " + describeAPIError(err)})
		return err
	}
	tail := withoutThinking(history[cut:])
	summary := anthropic.NewBetaTextBlock("<summary of the conversation before this point>\n" +
		strings.TrimSpace(result.Summary) + "\n</summary>")
	first := tail[0]
	first.Content = append([]anthropic.BetaContentBlockParamUnion{summary}, first.Content...)
	tail[0] = first
	if err := m.conversation.replaceHistory(tail); err != nil {
		m.addEntry(protocol.ManagerEntry{Role: "error", Text: "Couldn't compact the conversation: " + err.Error()})
		return err
	}
	m.mu.Lock()
	m.promptTokens = 0
	m.mu.Unlock()
	kept := m.keepRemembered(result.Remember)
	text := "Conversation compacted"
	if kept > 0 {
		text += fmt.Sprintf(" · kept %d in private notes", kept)
	}
	m.addEntry(protocol.ManagerEntry{Role: "compacted", Text: text})
	return nil
}

// tailStart is where the turns kept word for word begin: at the user's
// words keepTurns turns back, or one turn back when those are too large.
// Zero means there is nothing before them to compact.
func tailStart(history []anthropic.BetaMessageParam) int {
	var starts []int
	for i, message := range history {
		if message.Role == anthropic.BetaMessageParamRoleUser && hasUserWords(message) {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		return 0
	}
	at := starts[max(0, len(starts)-keepTurns)]
	if estimateTokens(history[at:]) > maxKeptTail {
		at = starts[len(starts)-1]
	}
	return at
}

func hasUserWords(message anthropic.BetaMessageParam) bool {
	for _, block := range message.Content {
		if block.OfText != nil && strings.TrimSpace(block.OfText.Text) != "" {
			return true
		}
	}
	return false
}

// withoutThinking drops the thinking from kept turns: written against the
// history before them, it no longer fits once that is a summary.
func withoutThinking(history []anthropic.BetaMessageParam) []anthropic.BetaMessageParam {
	out := make([]anthropic.BetaMessageParam, 0, len(history))
	for _, message := range history {
		if message.Role == anthropic.BetaMessageParamRoleAssistant {
			kept := message.Content[:0:0]
			for _, block := range message.Content {
				if block.OfThinking != nil || block.OfRedactedThinking != nil || block.OfCompaction != nil {
					continue
				}
				kept = append(kept, block)
			}
			if len(kept) == 0 {
				kept = append(kept, anthropic.NewBetaTextBlock("(thought it over)"))
			}
			message.Content = kept
		}
		out = append(out, message)
	}
	return out
}

type summary struct {
	Summary  string       `json:"summary"`
	Remember []remembered `json:"remember"`
}

// summarise asks the chat model for a summary of history and the lasting
// things in it, against the private notes already kept.
func (m *Manager) summarise(ctx context.Context, model Model, history []anthropic.BetaMessageParam, full bool) (summary, error) {
	var b strings.Builder
	b.WriteString("<transcript>\n")
	b.WriteString(transcript(history))
	b.WriteString("\n</transcript>\n\n<your private notes>\n")
	b.WriteString(m.privateNotesIndex(4000))
	b.WriteString("\n</your private notes>")
	if !full {
		b.WriteString("\n\nThis conversation is ending; a new one starts. The summary is not needed: leave it empty.")
	}
	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(m.currentSettings().ChatModel),
		MaxTokens: 6000,
		System:    []anthropic.BetaTextBlockParam{{Text: compactPrompt}},
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(b.String()))},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffortLow,
			Format: anthropic.BetaJSONOutputFormatParam{Schema: compactSchema},
		},
	}
	reply, err := model.Stream(ctx, params, nil)
	if err != nil {
		return summary{}, err
	}
	var out summary
	if err := json.Unmarshal([]byte(strings.TrimSpace(replyText(reply))), &out); err != nil {
		return summary{}, fmt.Errorf("the summary wasn't readable: %w", err)
	}
	if full && strings.TrimSpace(out.Summary) == "" {
		return summary{}, fmt.Errorf("the summary came back empty")
	}
	return out, nil
}

// transcript is the history as plain words: who said what, the tools used
// and the start of what they returned, the latest part when it is long.
func transcript(history []anthropic.BetaMessageParam) string {
	var lines []string
	for _, message := range history {
		raw, err := json.Marshal(message)
		if err != nil {
			continue
		}
		var wire claudeTurn
		if json.Unmarshal(raw, &wire) != nil {
			continue
		}
		parts, err := claudeParts(wire.Content)
		if err != nil {
			continue
		}
		speaker := "User"
		if message.Role == anthropic.BetaMessageParamRoleAssistant {
			speaker = "Agentman"
		}
		for _, part := range parts {
			switch part.Type {
			case "text":
				if text := strings.TrimSpace(part.Text); text != "" {
					lines = append(lines, speaker+": "+text)
				}
			case "image":
				lines = append(lines, speaker+": [an image]")
			case "tool_use":
				lines = append(lines, fmt.Sprintf("[Agentman used %s %s]", part.Name, clipText(string(part.Input), 300)))
			case "tool_result":
				text, images := resultContent(part.Content)
				line := "[result: " + clipText(strings.TrimSpace(text), 600)
				if len(images) > 0 {
					line += " (with an image)"
				}
				lines = append(lines, line+"]")
			case "compaction":
				var earlier string
				if json.Unmarshal(part.Content, &earlier) == nil && earlier != "" {
					lines = append(lines, "[summary of what came before: "+earlier+"]")
				}
			}
		}
	}
	text := strings.Join(lines, "\n")
	if len(text) > maxTranscript {
		text = "[earlier part left out]\n" + text[len(text)-maxTranscript:]
	}
	return text
}

func clipText(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "…"
}

// privateNotesIndex lists the private notes, a line each, within limit.
func (m *Manager) privateNotesIndex(limit int) string {
	if m.knowledge == nil {
		return "(none)"
	}
	notes, err := m.knowledge.List(knowledge.Private)
	if err != nil || len(notes) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for _, listed := range notes {
		note, err := m.knowledge.Read(knowledge.Private, listed.Name)
		if err != nil {
			continue
		}
		line := fmt.Sprintf("- %s (%s): %s\n", note.Title, note.Name, clipText(strings.Join(strings.Fields(note.Body), " "), 200))
		if b.Len()+len(line) > limit {
			break
		}
		b.WriteString(line)
	}
	return strings.TrimSpace(b.String())
}

// keepRemembered writes what a summary said to remember into the private
// notes, updating a note of the same name or title, and says how many.
func (m *Manager) keepRemembered(items []remembered) int {
	kept := 0
	for _, item := range items {
		if kept >= maxRemembered {
			break
		}
		if _, err := m.savePrivateNote(item.Title, item.Body, item.Name); err == nil {
			kept++
		}
	}
	return kept
}

// savePrivateNote writes one of Agentman's own notes: a new one, or the one
// named, or the one with the same title.
func (m *Manager) savePrivateNote(title, body, name string) (knowledge.Note, error) {
	title, body, name = strings.TrimSpace(title), strings.TrimSpace(body), strings.TrimSpace(name)
	if title == "" || body == "" {
		return knowledge.Note{}, fmt.Errorf("a note needs a title and a body")
	}
	if utf8.RuneCountInString(title) > maxNoteTitle || utf8.RuneCountInString(body) > maxNoteBody {
		return knowledge.Note{}, fmt.Errorf("keep a note short: a title in a line and a body of a few paragraphs at most")
	}
	if m.knowledge == nil {
		return knowledge.Note{}, fmt.Errorf("notes aren't kept on this Mac")
	}
	if name != "" {
		if _, err := m.knowledge.Read(knowledge.Private, name); err != nil {
			name = ""
		}
	}
	if name == "" {
		notes, _ := m.knowledge.List(knowledge.Private)
		for _, note := range notes {
			if strings.EqualFold(note.Title, title) {
				name = note.Name
				break
			}
		}
	}
	saved, err := m.knowledge.Write(knowledge.Note{Name: name, Tier: knowledge.Private, Title: title, Body: body})
	if err != nil {
		return knowledge.Note{}, err
	}
	m.emitNotes()
	return saved, nil
}

// emitNotes tells the apps the notes changed.
func (m *Manager) emitNotes() {
	if m.knowledge == nil {
		return
	}
	notes, err := m.knowledge.List("")
	if err != nil {
		return
	}
	m.emit(protocol.Event{Type: protocol.EvtKnowledge, Notes: protocolNotes(notes)})
}

func protocolNotes(notes []knowledge.Note) []protocol.Note {
	out := make([]protocol.Note, 0, len(notes))
	for _, note := range notes {
		out = append(out, protocol.Note{Name: note.Name, Tier: note.Tier, Title: note.Title, Always: note.Always, UpdatedAt: note.UpdatedAt})
	}
	return out
}

func runSavePrivateNote(_ context.Context, m *Manager, input map[string]any) toolResult {
	title, _ := input["title"].(string)
	body, _ := input["body"].(string)
	name, _ := input["name"].(string)
	saved, err := m.savePrivateNote(title, body, name)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	return toolResult{
		text:     fmt.Sprintf("Kept in your private notes as %s.", saved.Name),
		activity: "Kept a private note: " + saved.Title,
	}
}

func runDeletePrivateNote(_ context.Context, m *Manager, input map[string]any) toolResult {
	name, _ := input["name"].(string)
	name = strings.TrimSpace(name)
	if m.knowledge == nil || name == "" {
		return toolResult{text: "Name the private note to delete.", isError: true}
	}
	note, err := m.knowledge.Read(knowledge.Private, name)
	if err != nil {
		return toolResult{text: "There is no private note called " + name + ".", isError: true}
	}
	if err := m.knowledge.Delete(knowledge.Private, name); err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	m.emitNotes()
	return toolResult{text: "Deleted.", activity: "Deleted a private note: " + note.Title}
}

// reset starts a new conversation. The one before is archived; the cards
// still waiting come along. What lasts of it is kept in the private notes
// meanwhile, in the background.
func (m *Manager) reset(ctx context.Context) protocol.Event {
	m.mu.Lock()
	stop := m.stopTurn
	m.mu.Unlock()
	if stop != nil {
		stop()
		for range 50 {
			m.mu.Lock()
			running := m.stopTurn != nil
			m.mu.Unlock()
			if !running {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	m.compactMu.Lock()
	defer m.compactMu.Unlock()
	before := m.conversation.messages()
	if err := m.conversation.reset(); err != nil {
		return protocol.Event{Type: protocol.EvtError, Error: "Couldn't start a new chat: " + err.Error()}
	}
	m.mu.Lock()
	m.notes = nil
	m.promptTokens = 0
	m.fresh = true
	m.mu.Unlock()
	event := protocol.Event{Type: protocol.EvtManagerReset}
	m.emit(event)
	if len(before) > 0 {
		if model, err := m.model(ctx); err == nil {
			go m.carryForward(model, before)
		}
	}
	return event
}

// freshStart is what opens the first turn of a new conversation: the
// private notes, as they stand then, with what the last conversation left
// in them. Empty for any other turn.
func (m *Manager) freshStart() string {
	m.mu.Lock()
	fresh := m.fresh
	m.fresh = false
	m.mu.Unlock()
	if !fresh {
		return ""
	}
	index := m.privateNotesIndex(3000)
	if index == "(none)" {
		return "This is a new conversation."
	}
	return "This is a new conversation. Your private notes, from earlier ones:\n" + index
}

// carryForward keeps what lasts of a conversation that ended in the
// private notes.
func (m *Manager) carryForward(model Model, history []anthropic.BetaMessageParam) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := m.summarise(ctx, model, history, false)
	if err != nil {
		return
	}
	if kept := m.keepRemembered(result.Remember); kept > 0 {
		m.addEntry(protocol.ManagerEntry{
			Role: "activity", Tool: "save_private_note",
			Text: fmt.Sprintf("Kept %d %s from the last conversation in private notes", kept, map[bool]string{true: "thing", false: "things"}[kept == 1]),
		})
	}
}
