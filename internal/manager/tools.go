package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/lenajeremy/agentman/internal/knowledge"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// Agents is the daemon, as far as Agentman needs it: the agents it is
// watching, and its own request handler, through which Agentman reads them
// exactly as an app would.
type Agents interface {
	Sessions() []protocol.Session
	Request(ctx context.Context, req protocol.Request) protocol.Event
}

// Bounds on what one tool call hands the model, so one long transcript or
// diff cannot crowd out the conversation.
const (
	maxToolText       = 30_000
	maxMessageText    = 3_000
	defaultReadLimit  = 30
	maxReadLimit      = 80
	defaultCommitPage = 20
)

// toolResult is what running a tool produced: what the model reads, and what
// the user is told Agentman did.
type toolResult struct {
	text      string
	image     *anthropic.BetaBase64ImageSourceParam
	isError   bool
	activity  string
	sessionID string
	// path is an image the user should see too.
	path string
}

type toolDef struct {
	name        string
	description string
	properties  map[string]any
	required    []string
	run         func(ctx context.Context, m *Manager, input map[string]any) toolResult
}

var agentProperty = map[string]any{
	"type":        "string",
	"description": "The agent: its id from list_agents, or its name.",
}

// toolDefs is every tool Agentman has. It is filled in by init, not by
// its own initializer: the tools reach the turn loop (a reminder starts a
// turn), and the turn loop reads the tools.
var toolDefs []toolDef

func init() {
	toolDefs = []toolDef{
		{
			name:        "list_agents",
			description: "Every agent being watched: name, id, kind, folder, state, what it is asking if it needs the user, when it was last active, and your brief's summary.",
			properties:  map[string]any{},
			run:         runListAgents,
		},
		{
			name:        "get_brief",
			description: "Your full brief on one agent: goal, what it has done, what it is doing, what blocks it, what it needs from the user, and the files it is working on.",
			properties:  map[string]any{"agent": agentProperty},
			required:    []string{"agent"},
			run:         runGetBrief,
		},
		{
			name:        "read_messages",
			description: "What one agent and its user actually wrote, newest last: its replies, the user's messages, and its tool calls in one line each. Pass before, from a previous result, to read further back.",
			properties: map[string]any{
				"agent":  agentProperty,
				"limit":  map[string]any{"type": "integer", "description": "How many messages, up to 80. Default 30."},
				"before": map[string]any{"type": "string", "description": "Read messages older than this cursor, from a previous read_messages result."},
			},
			required: []string{"agent"},
			run:      runReadMessages,
		},
		{
			name:        "view_image",
			description: "Look at an image one agent took or opened, such as a screenshot, by the path it used. The user sees the image in the conversation too.",
			properties: map[string]any{
				"agent": agentProperty,
				"path":  map[string]any{"type": "string", "description": "The image's absolute path, as the agent used it."},
			},
			required: []string{"agent", "path"},
			run:      runViewImage,
		},
		{
			name:        "list_changes",
			description: "The files changed in one agent's folder since the last commit: staged, unstaged and new, with lines added and removed.",
			properties:  map[string]any{"agent": agentProperty},
			required:    []string{"agent"},
			run:         runListChanges,
		},
		{
			name:        "file_diff",
			description: "The uncommitted diff of one file in an agent's folder.",
			properties: map[string]any{
				"agent": agentProperty,
				"path":  map[string]any{"type": "string", "description": "The file's path relative to the agent's folder, as list_changes gave it."},
			},
			required: []string{"agent", "path"},
			run:      runFileDiff,
		},
		{
			name:        "list_commits",
			description: "The latest commits in one agent's folder: hash, subject, author and when.",
			properties:  map[string]any{"agent": agentProperty},
			required:    []string{"agent"},
			run:         runListCommits,
		},
		{
			name: "send_to_agent",
			description: "Send one agent a message, as its user, when the user asks you to. It is typed into the agent, " +
				"or queued until its current turn ends if it can only be reached between turns. " +
				"The user sees exactly what you sent.",
			properties: map[string]any{
				"agent": agentProperty,
				"text":  map[string]any{"type": "string", "description": "The message, written as the user would write it to the agent."},
			},
			required: []string{"agent", "text"},
			run:      runSendToAgent,
		},
		{
			name: "answer_question",
			description: "Answer the question or permission request an agent is showing, when the user asks you to. " +
				"Low-risk approvals and any \"no\" are sent at once. Anything else needs the user's OK: " +
				"your pick is marked on the question's card for them to confirm with one tap.",
			properties: map[string]any{
				"agent":  agentProperty,
				"option": map[string]any{"type": "string", "description": "The option to choose: its key or its label, from list_agents."},
				"text":   map[string]any{"type": "string", "description": "A typed answer, for a question that takes one, or a note with an option that carries one."},
			},
			required: []string{"agent"},
			run:      runAnswerQuestion,
		},
		{
			name:        "interrupt",
			description: "Stop what one agent is doing, like pressing Escape in its terminal, when the user asks you to.",
			properties:  map[string]any{"agent": agentProperty},
			required:    []string{"agent"},
			run:         runInterrupt,
		},
		{
			name: "dismiss_card",
			description: "Take away a card that no longer needs the user: they answered the agent themselves, " +
				"the agent moved on, or it was dealt with some other way. Cards are listed by list_agents.",
			properties: map[string]any{
				"card":   map[string]any{"type": "string", "description": "The card's id."},
				"reason": map[string]any{"type": "string", "description": "Why, in a few words: \"answered in the agent\"."},
			},
			required: []string{"card", "reason"},
			run:      runDismissCard,
		},
		{
			name: "search_knowledge",
			description: "Search the user's notes, shared and private: style guides, conventions, how they like things done, " +
				"facts about their projects. Returns the matching notes with the lines that matched.",
			properties: map[string]any{"query": map[string]any{"type": "string", "description": "Words to look for."}},
			required:   []string{"query"},
			run:        runSearchKnowledge,
		},
		{
			name:        "read_knowledge",
			description: "Read one of the user's notes in full, by the name search_knowledge gave.",
			properties:  map[string]any{"name": map[string]any{"type": "string"}},
			required:    []string{"name"},
			run:         runReadKnowledge,
		},
		{
			name: "save_knowledge",
			description: "Save a note when the user asks you to remember something, or to change a note. " +
				"Shared notes can be read by every agent; private ones only by you. " +
				"A shared note marked always goes into each project's instruction file when update_instructions runs.",
			properties: map[string]any{
				"title":  map[string]any{"type": "string"},
				"body":   map[string]any{"type": "string", "description": "The note, in Markdown."},
				"tier":   map[string]any{"type": "string", "enum": []string{"shared", "private"}, "description": "Default shared."},
				"always": map[string]any{"type": "boolean", "description": "Put it in every project's instructions. Default false."},
				"name":   map[string]any{"type": "string", "description": "An existing note's name, to change it. Leave out for a new note."},
			},
			required: []string{"title", "body"},
			run:      runSaveKnowledge,
		},
		{
			name: "list_keys",
			description: "The API keys you keep for the user's agents, by name: what each is for, whether it has a value, " +
				"and its rule (auto: give it to any agent; projects: only to listed folders without asking; ask: always ask the user). " +
				"You never see the values.",
			properties: map[string]any{},
			run:        runListKeys,
		},
		{
			name: "give_key",
			description: "Give an agent one of the keys, when the user asks: it is written into the .env in the agent's folder, " +
				"if git ignores .env. When the key's rule says to ask, or .env isn't ignored, the user gets a card instead. " +
				"After it is given, tell the agent with send_to_agent which variable to read.",
			properties: map[string]any{
				"agent":  agentProperty,
				"key":    map[string]any{"type": "string", "description": "The key's name, like ELEVENLABS_API_KEY."},
				"reason": map[string]any{"type": "string", "description": "Why the agent needs it, for the user if they are asked."},
			},
			required: []string{"agent", "key"},
			run:      runGiveKey,
		},
		{
			name: "update_instructions",
			description: "Write the user's shared notes marked always into one agent's project instructions " +
				"(CLAUDE.md for Claude Code, AGENTS.md for the others), in a block Agentman keeps; the rest of the file is untouched. " +
				"Only when the user asks.",
			properties: map[string]any{"agent": agentProperty},
			required:   []string{"agent"},
			run:        runUpdateInstructions,
		},
		{
			name: "schedule_reminder",
			description: "Leave yourself a reminder: when it comes due, you take a turn on your own with it, without the user writing. " +
				"Once, or repeating, such as \"check agentman-58 in 10 minutes\" or \"every 30 minutes, tell the user what changed\". " +
				"Every wake-up is a paid turn, so use the longest interval that does the job. A repeat with neither times nor until stops after 12 hours.",
			properties: map[string]any{
				"message":       map[string]any{"type": "string", "description": "What to do when it comes due, written to yourself."},
				"in_minutes":    map[string]any{"type": "number", "description": "Minutes from now until it first fires."},
				"at":            map[string]any{"type": "string", "description": "Instead of in_minutes: a local time, HH:MM (the next one to come), or an ISO 8601 time."},
				"every_minutes": map[string]any{"type": "number", "description": "Repeat this often, at least 1. Leave out for once."},
				"times":         map[string]any{"type": "integer", "description": "With every_minutes: how many times in all."},
				"until":         map[string]any{"type": "string", "description": "With every_minutes: stop after this local time, HH:MM or ISO 8601."},
			},
			required: []string{"message"},
			run:      runScheduleReminder,
		},
		{
			name: "watch_agent",
			description: "Watch an agent and get the result back as a message when it gets there: its turn ends, it needs the user, it is idle, or it ends. " +
				"Use it instead of checking repeatedly, after you send an agent work, or when the user wants to know once something is done. " +
				"If it doesn't happen in time, you hear that too.",
			properties: map[string]any{
				"agent":           agentProperty,
				"until":           map[string]any{"type": "string", "enum": []string{"turn_end", "needs_user", "idle", "ended"}},
				"note":            map[string]any{"type": "string", "description": "Why you are watching and what to do then, for when the result comes back."},
				"timeout_minutes": map[string]any{"type": "number", "description": "Give up after this long. Default 60, at most 1440."},
			},
			required: []string{"agent", "until"},
			run:      runWatchAgent,
		},
		{
			name: "rename_agent",
			description: "Give an agent the display name the user asks for, everywhere it shows: the Mac, the phone, notifications and your tools. " +
				"Its id stays the same; an empty name gives it back the agent's own.",
			properties: map[string]any{
				"agent": agentProperty,
				"name":  map[string]any{"type": "string", "description": "The new name, up to 80 characters; empty for the agent's own."},
			},
			required: []string{"agent", "name"},
			run:      runRenameAgent,
		},
		{
			name:        "list_schedules",
			description: "Your reminders and watches: what each is for, when it comes due, and its id.",
			properties:  map[string]any{},
			run:         runListSchedules,
		},
		{
			name: "update_schedule",
			description: "Change one of your reminders or watches in place, by its id: a reminder's time, repeat or message, " +
				"or a watch's timeout or note. Leave out what stays the same.",
			properties: map[string]any{
				"id":              map[string]any{"type": "string"},
				"message":         map[string]any{"type": "string", "description": "A reminder's new message, or a watch's new note."},
				"in_minutes":      map[string]any{"type": "number", "description": "A reminder's new time, in minutes from now."},
				"at":              map[string]any{"type": "string", "description": "A reminder's new time: HH:MM or ISO 8601."},
				"every_minutes":   map[string]any{"type": "number", "description": "A reminder's new interval; 0 makes it fire once."},
				"times":           map[string]any{"type": "integer", "description": "A repeat's new count in all."},
				"until":           map[string]any{"type": "string", "description": "A repeat's new end: HH:MM or ISO 8601."},
				"timeout_minutes": map[string]any{"type": "number", "description": "A watch's new timeout, in minutes from now."},
			},
			required: []string{"id"},
			run:      runUpdateSchedule,
		},
		{
			name:        "cancel_schedule",
			description: "Cancel one of your reminders or watches, by its id from list_schedules.",
			properties:  map[string]any{"id": map[string]any{"type": "string"}},
			required:    []string{"id"},
			run:         runCancelSchedule,
		},
	}
}

// apiTools is the tool list as the API takes it. Built once, in a fixed
// order, so the request prefix stays cacheable.
func apiTools() []anthropic.BetaToolUnionParam {
	tools := make([]anthropic.BetaToolUnionParam, 0, len(toolDefs))
	for _, def := range toolDefs {
		tool := anthropic.BetaToolParam{
			Name:        def.name,
			Description: anthropic.String(def.description),
			InputSchema: anthropic.BetaToolInputSchemaParam{
				Properties: def.properties,
				Required:   def.required,
			},
		}
		tools = append(tools, anthropic.BetaToolUnionParam{OfTool: &tool})
	}
	return tools
}

func (m *Manager) runTool(ctx context.Context, name string, rawInput []byte) toolResult {
	input := map[string]any{}
	if len(rawInput) > 0 {
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return toolResult{text: "The input was not valid JSON.", isError: true}
		}
	}
	for _, def := range toolDefs {
		if def.name == name {
			return def.run(ctx, m, input)
		}
	}
	return toolResult{text: "There is no tool called " + name + ".", isError: true}
}

// resolveAgent finds the agent an input names, by id or by name.
func (m *Manager) resolveAgent(input map[string]any) (protocol.Session, error) {
	query, _ := input["agent"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return protocol.Session{}, fmt.Errorf("say which agent")
	}
	sessions := m.agents.Sessions()
	for _, s := range sessions {
		if s.ID == query {
			return s, nil
		}
	}
	// By the name it shows, or by its agent's own name when the user renamed
	// it: an agent stays findable by the name it had a minute ago.
	var matches []protocol.Session
	for _, s := range sessions {
		if strings.EqualFold(s.Name, query) || (s.OriginalName != "" && strings.EqualFold(s.OriginalName, query)) {
			matches = append(matches, s)
		}
	}
	if len(matches) == 0 {
		lower := strings.ToLower(query)
		for _, s := range sessions {
			if strings.Contains(strings.ToLower(s.Name), lower) ||
				(s.OriginalName != "" && strings.Contains(strings.ToLower(s.OriginalName), lower)) {
				matches = append(matches, s)
			}
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return protocol.Session{}, fmt.Errorf("no agent called %q is running; list_agents shows who is", query)
	default:
		var names []string
		for _, s := range matches {
			names = append(names, fmt.Sprintf("%s (%s)", s.Name, s.ID))
		}
		return protocol.Session{}, fmt.Errorf("%q matches several agents: %s; use an id", query, strings.Join(names, ", "))
	}
}

func runListAgents(_ context.Context, m *Manager, _ map[string]any) toolResult {
	sessions := m.agents.Sessions()
	if len(sessions) == 0 {
		return toolResult{text: "No agents are running.", activity: "Checked the agents"}
	}
	cards := m.conversation.openCards()
	var b strings.Builder
	now := time.Now()
	for _, s := range sessions {
		fmt.Fprintf(&b, "- %s (%s, %s) — %s", s.Name, s.Kind, shortPath(s.Cwd), stateText(s))
		if s.OriginalName != "" {
			fmt.Fprintf(&b, "; the user renamed it from %q", s.OriginalName)
		}
		if s.LastActivityAt > 0 {
			fmt.Fprintf(&b, ", last active %s", ago(now, s.LastActivityAt))
		}
		fmt.Fprintf(&b, "\n  id: %s", s.ID)
		if s.Model != "" {
			fmt.Fprintf(&b, ", model: %s", s.Model)
		}
		if brief, ok := m.briefs.get(s.ID); ok && brief.Summary != "" {
			fmt.Fprintf(&b, "\n  brief: %s", brief.Summary)
		}
		if s.Question != nil && len(s.Question.Options) > 0 {
			var options []string
			for _, option := range s.Question.Options {
				options = append(options, fmt.Sprintf("%s=%q", option.Key, option.Label))
			}
			fmt.Fprintf(&b, "\n  options: %s", strings.Join(options, ", "))
		}
		for _, card := range cards {
			if card.Card.SessionID == s.ID {
				fmt.Fprintf(&b, "\n  card %s, waiting on the user: %s", card.Card.ID, card.Text)
			}
		}
		b.WriteString("\n")
	}
	agents := "agent"
	if len(sessions) != 1 {
		agents = "agents"
	}
	return toolResult{text: b.String(), activity: fmt.Sprintf("Checked %d %s", len(sessions), agents)}
}

func runGetBrief(_ context.Context, m *Manager, input map[string]any) toolResult {
	session, err := m.resolveAgent(input)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	brief, ok := m.briefs.get(session.ID)
	if !ok {
		return toolResult{
			text:      fmt.Sprintf("No brief on %s yet. Use read_messages to see what it wrote.", session.Name),
			activity:  "Looked for a brief on " + session.Name,
			sessionID: session.ID,
		}
	}
	raw, _ := json.MarshalIndent(brief, "", "  ")
	return toolResult{text: string(raw), activity: "Read the brief on " + session.Name, sessionID: session.ID}
}

func runReadMessages(ctx context.Context, m *Manager, input map[string]any) toolResult {
	session, err := m.resolveAgent(input)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	limit := defaultReadLimit
	if value, ok := input["limit"].(float64); ok && value > 0 {
		limit = min(int(value), maxReadLimit)
	}
	before, _ := input["before"].(string)
	event := m.agents.Request(ctx, protocol.Request{
		Type: protocol.ReqFetchMessages, SessionID: session.ID, Limit: limit, Before: before,
	})
	if event.Type == protocol.EvtError || event.Page == nil {
		return toolResult{text: "Couldn't read " + session.Name + ": " + event.Error, isError: true}
	}
	text := formatTranscript(event.Page.Messages)
	if event.Page.HasMore && event.Page.NextCursor != "" {
		text += fmt.Sprintf("\n(Older messages exist: pass before=%q to read them.)", event.Page.NextCursor)
	}
	return toolResult{text: text, activity: "Read " + session.Name, sessionID: session.ID}
}

func runViewImage(ctx context.Context, m *Manager, input map[string]any) toolResult {
	session, err := m.resolveAgent(input)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	path, _ := input["path"].(string)
	if !filepath.IsAbs(path) {
		return toolResult{text: "Give the image's absolute path, as the agent used it.", isError: true}
	}
	event := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqReadSeenFile, SessionID: session.ID, Path: path})
	if event.Type == protocol.EvtError || event.Workspace == nil {
		return toolResult{text: "Couldn't open that image: " + event.Error, isError: true}
	}
	media := anthropic.BetaBase64ImageSourceMediaType(event.Workspace.MIME)
	switch event.Workspace.MIME {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return toolResult{text: "That file isn't an image the model can read (" + event.Workspace.MIME + ").", isError: true}
	}
	if event.Workspace.Image == "" {
		return toolResult{text: "That image is too large to look at.", isError: true}
	}
	return toolResult{
		image:     &anthropic.BetaBase64ImageSourceParam{Data: event.Workspace.Image, MediaType: media},
		text:      fmt.Sprintf("The image at %s, from %s.", path, session.Name),
		activity:  fmt.Sprintf("Looked at %s from %s", filepath.Base(path), session.Name),
		sessionID: session.ID,
		path:      path,
	}
}

func runListChanges(ctx context.Context, m *Manager, input map[string]any) toolResult {
	session, err := m.resolveAgent(input)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	event := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqListChanges, SessionID: session.ID})
	if event.Type == protocol.EvtError || event.Workspace == nil {
		return toolResult{text: "Couldn't list the changes: " + event.Error, isError: true}
	}
	changes := event.Workspace.Changes
	if len(changes) == 0 {
		return toolResult{text: "No uncommitted changes in " + shortPath(session.Cwd) + ".", activity: "Checked " + session.Name + "'s changes", sessionID: session.ID}
	}
	var b strings.Builder
	for _, change := range changes {
		fmt.Fprintf(&b, "- %s (%s, +%d −%d)\n", change.Path, change.Status, change.Added, change.Removed)
	}
	if event.Workspace.Hidden > 0 {
		fmt.Fprintf(&b, "(%d private files are left out.)\n", event.Workspace.Hidden)
	}
	return toolResult{text: b.String(), activity: "Checked " + session.Name + "'s changes", sessionID: session.ID}
}

func runFileDiff(ctx context.Context, m *Manager, input map[string]any) toolResult {
	session, err := m.resolveAgent(input)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	path, _ := input["path"].(string)
	event := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqFileDiff, SessionID: session.ID, Path: path})
	if event.Type == protocol.EvtError || event.Workspace == nil {
		return toolResult{text: "Couldn't read that diff: " + event.Error, isError: true}
	}
	diff := event.Workspace.Diff
	if diff == "" {
		diff = "No changes in " + path + "."
	}
	return toolResult{text: clip(diff, maxToolText), activity: "Read the diff of " + filepath.Base(path), sessionID: session.ID}
}

func runListCommits(ctx context.Context, m *Manager, input map[string]any) toolResult {
	session, err := m.resolveAgent(input)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	event := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqListCommits, SessionID: session.ID})
	if event.Type == protocol.EvtError || event.Workspace == nil {
		return toolResult{text: "Couldn't read the commits: " + event.Error, isError: true}
	}
	var b strings.Builder
	now := time.Now()
	if branch := event.Workspace.Branch; branch != "" {
		fmt.Fprintf(&b, "On branch %s.\n", branch)
	}
	for i, commit := range event.Workspace.Commits {
		if i == defaultCommitPage {
			break
		}
		fmt.Fprintf(&b, "- %s %s (%s, %s)\n", commit.Hash[:min(7, len(commit.Hash))], commit.Subject, commit.Author, ago(now, commit.AuthoredAt))
	}
	return toolResult{text: b.String(), activity: "Read " + session.Name + "'s commits", sessionID: session.ID}
}

func runSendToAgent(ctx context.Context, m *Manager, input map[string]any) toolResult {
	session, err := m.resolveAgent(input)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	text, _ := input["text"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return toolResult{text: "Say what to send.", isError: true}
	}
	if session.Inject == "" || session.Inject == protocol.InjectNone {
		return toolResult{text: session.Name + " can't be messaged from here; the user has to open it.", isError: true}
	}
	status, err := m.sendTo(ctx, session, text)
	if err != nil {
		return toolResult{text: "Couldn't send it: " + err.Error(), isError: true}
	}
	result := "Sent. " + session.Name + " has it."
	if status == protocol.StatusQueued {
		result = "Queued: " + session.Name + " reads it when its current turn ends."
	}
	return toolResult{
		text:      result,
		activity:  fmt.Sprintf("Sent %s: “%s”", session.Name, clip(text, 120)),
		sessionID: session.ID,
	}
}

func runAnswerQuestion(ctx context.Context, m *Manager, input map[string]any) toolResult {
	session, err := m.resolveAgent(input)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	q := session.Question
	if q == nil {
		return toolResult{text: session.Name + " isn't asking anything right now.", isError: true}
	}
	choice, _ := input["option"].(string)
	text, _ := input["text"].(string)
	text = strings.TrimSpace(text)
	key := ""
	if choice = strings.TrimSpace(choice); choice != "" {
		for _, option := range q.Options {
			if option.Key == choice || strings.EqualFold(option.Label, choice) {
				key = option.Key
				break
			}
		}
		if key == "" {
			return toolResult{text: fmt.Sprintf("%s has no option %q; list_agents shows the options.", session.Name, choice), isError: true}
		}
	}
	if key == "" && text == "" {
		return toolResult{text: "Say which option, or what to answer.", isError: true}
	}

	// Approving is for the rules to decide, not the model: only what the
	// user let Agentman approve, or a no, goes straight to the agent.
	d := classify(session, q)
	safe := key != "" && ((d.auto && key == d.optionKey) || isDenial(q.Options, key))
	m.cardMu.Lock()
	defer m.cardMu.Unlock()
	if !safe {
		if key == "" {
			m.postQuestionCardLocked(session, d, "")
			return toolResult{
				text:      "A typed answer needs the user. Their card for this question is up; tell them what you'd write.",
				activity:  "Left " + session.Name + "'s question to you",
				sessionID: session.ID,
			}
		}
		m.postQuestionCardLocked(session, d, key)
		reason := d.reason
		if reason == "" {
			reason = "only the user can approve that"
		}
		return toolResult{
			text: fmt.Sprintf("Not sent: that needs the user's OK (%s). Your pick is marked on the card, "+
				"so it takes them one tap. Tell them it's there.", reason),
			activity:  "Marked a pick on " + session.Name + "'s question for you",
			sessionID: session.ID,
		}
	}
	if err := m.answer(ctx, session.ID, protocol.Request{QuestionID: q.ID, OptionKey: key, AnswerText: text}); err != nil {
		return toolResult{text: "Couldn't answer: " + err.Error(), isError: true}
	}
	chosen := chosenText(q.Options, protocol.Request{OptionKey: key, AnswerText: text})
	if open := m.openCardLocked(session.ID, q.ID); open != nil {
		m.closeCardLocked(*open, protocol.CardSettled, "Agentman answered "+chosen)
	}
	return toolResult{
		text:      "Answered " + chosen + ".",
		activity:  fmt.Sprintf("Answered %s: %s", session.Name, chosen),
		sessionID: session.ID,
	}
}

func runInterrupt(ctx context.Context, m *Manager, input map[string]any) toolResult {
	session, err := m.resolveAgent(input)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	event := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqInterrupt, SessionID: session.ID})
	if event.Type == protocol.EvtError || event.Status == protocol.StatusFailed {
		return toolResult{text: "Couldn't stop it: " + strings.TrimPrefix(event.Error, "daemon: "), isError: true}
	}
	return toolResult{text: "Stopped " + session.Name + ".", activity: "Stopped " + session.Name, sessionID: session.ID}
}

func runDismissCard(_ context.Context, m *Manager, input map[string]any) toolResult {
	id, _ := input["card"].(string)
	reason, _ := input["reason"].(string)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "No longer needed"
	}
	m.cardMu.Lock()
	defer m.cardMu.Unlock()
	entry, ok := m.cardByIDLocked(strings.TrimSpace(id))
	if !ok {
		return toolResult{text: "No open card has that id; it may already be gone.", isError: true}
	}
	m.closeCardLocked(entry, protocol.CardSettled, capitalize(reason))
	return toolResult{
		text:      "Taken away.",
		activity:  fmt.Sprintf("Took away %s's card: %s", entry.Card.AgentName, reason),
		sessionID: entry.Card.SessionID,
	}
}

func runSearchKnowledge(_ context.Context, m *Manager, input map[string]any) toolResult {
	query, _ := input["query"].(string)
	hits, err := m.knowledge.Search(query, "", 8)
	if err != nil {
		return toolResult{text: strings.TrimPrefix(err.Error(), "knowledge: "), isError: true}
	}
	if len(hits) == 0 {
		return toolResult{text: "No notes match.", activity: "Searched your notes"}
	}
	var b strings.Builder
	for _, hit := range hits {
		fmt.Fprintf(&b, "## %s (name: %s, %s)\n%s\n\n", hit.Title, hit.Name, hit.Tier, hit.Snippet)
	}
	return toolResult{text: b.String(), activity: "Searched your notes"}
}

func runReadKnowledge(_ context.Context, m *Manager, input map[string]any) toolResult {
	name, _ := input["name"].(string)
	note, err := m.knowledge.Read("", strings.TrimSpace(name))
	if err != nil {
		return toolResult{text: "No note called " + name + "; search_knowledge finds them.", isError: true}
	}
	return toolResult{
		text:     fmt.Sprintf("# %s (%s, always: %v)\n\n%s", note.Title, note.Tier, note.Always, note.Body),
		activity: "Read your note “" + note.Title + "”",
	}
}

func runSaveKnowledge(_ context.Context, m *Manager, input map[string]any) toolResult {
	title, _ := input["title"].(string)
	body, _ := input["body"].(string)
	tier, _ := input["tier"].(string)
	name, _ := input["name"].(string)
	always, _ := input["always"].(bool)
	if tier == "" {
		tier = knowledge.Shared
	}
	saved, err := m.knowledge.Write(knowledge.Note{Name: strings.TrimSpace(name), Tier: tier, Title: title, Body: body, Always: always})
	if err != nil {
		return toolResult{text: strings.TrimPrefix(err.Error(), "knowledge: "), isError: true}
	}
	where := "shared with your agents"
	if saved.Tier == knowledge.Private {
		where = "private"
	}
	return toolResult{
		text:     fmt.Sprintf("Saved as %s (%s).", saved.Name, saved.Tier),
		activity: fmt.Sprintf("Saved a note, “%s”, %s", saved.Title, where),
	}
}

func runListKeys(_ context.Context, m *Manager, _ map[string]any) toolResult {
	keys := m.keys.list()
	if len(keys) == 0 {
		return toolResult{text: "No keys are kept yet. The user adds them in Agentman's settings, under Keys.", activity: "Checked your keys"}
	}
	var b strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&b, "- %s, rule %s", key.Name, key.Rule)
		if len(key.Projects) > 0 {
			var folders []string
			for _, project := range key.Projects {
				folders = append(folders, shortPath(project))
			}
			fmt.Fprintf(&b, " (%s)", strings.Join(folders, ", "))
		}
		if !key.Set {
			b.WriteString(", no value yet")
		}
		if key.Description != "" {
			fmt.Fprintf(&b, ": %s", key.Description)
		}
		b.WriteString("\n")
	}
	return toolResult{text: b.String(), activity: "Checked your keys"}
}

func runGiveKey(ctx context.Context, m *Manager, input map[string]any) toolResult {
	session, err := m.resolveAgent(input)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	name, _ := input["key"].(string)
	reason, _ := input["reason"].(string)
	if reason == "" {
		reason = "You asked Agentman to give it."
	}
	text, err := m.giveKey(ctx, session, name, reason)
	if err != nil {
		return toolResult{text: "Couldn't give it: " + err.Error(), isError: true}
	}
	return toolResult{text: text, sessionID: session.ID}
}

func runUpdateInstructions(_ context.Context, m *Manager, input map[string]any) toolResult {
	session, err := m.resolveAgent(input)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	path, count, err := m.knowledge.SyncInstructions(session.Cwd, string(session.Kind))
	if err != nil {
		return toolResult{text: "Couldn't update the instructions: " + err.Error(), isError: true}
	}
	notes := fmt.Sprintf("%d notes", count)
	if count == 1 {
		notes = "1 note"
	}
	if count == 0 {
		return toolResult{
			text:      "No shared notes are marked always, so there is nothing to add; any old block was taken out of " + shortPath(path) + ".",
			activity:  "Checked " + session.Name + "'s instructions",
			sessionID: session.ID,
		}
	}
	return toolResult{
		text:      fmt.Sprintf("Wrote %s into %s. The agent reads it at its next start, or tell it to reread the file.", notes, shortPath(path)),
		activity:  fmt.Sprintf("Put %s in %s's %s", notes, session.Name, filepath.Base(path)),
		sessionID: session.ID,
	}
}

func runScheduleReminder(_ context.Context, m *Manager, input map[string]any) toolResult {
	message, _ := input["message"].(string)
	now := m.now()
	var at time.Time
	if minutes, ok := input["in_minutes"].(float64); ok {
		at = now.Add(time.Duration(minutes * float64(time.Minute)))
	} else if text, _ := input["at"].(string); strings.TrimSpace(text) != "" {
		parsed, err := parseWhen(text, now)
		if err != nil {
			return toolResult{text: err.Error(), isError: true}
		}
		at = parsed
	} else {
		return toolResult{text: "Say when: in_minutes, or at.", isError: true}
	}
	var every time.Duration
	if minutes, ok := input["every_minutes"].(float64); ok {
		every = time.Duration(minutes * float64(time.Minute))
	}
	times := 0
	if value, ok := input["times"].(float64); ok {
		times = int(value)
	}
	var until time.Time
	if text, _ := input["until"].(string); strings.TrimSpace(text) != "" {
		parsed, err := parseWhen(text, now)
		if err != nil {
			return toolResult{text: err.Error(), isError: true}
		}
		until = parsed
	}
	item, err := m.remind(message, at, every, times, until)
	if err != nil {
		return toolResult{text: "Couldn't schedule it: " + err.Error(), isError: true}
	}
	return toolResult{
		text:     fmt.Sprintf("Scheduled [%s]: %s. The user sees it and can cancel it.", item.ID, scheduleLabel(item)),
		activity: "Set a reminder: " + scheduleLabel(item),
	}
}

func runWatchAgent(_ context.Context, m *Manager, input map[string]any) toolResult {
	session, err := m.resolveAgent(input)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	condition, _ := input["until"].(string)
	note, _ := input["note"].(string)
	timeout := time.Duration(0)
	if minutes, ok := input["timeout_minutes"].(float64); ok {
		timeout = time.Duration(minutes * float64(time.Minute))
	}
	item, err := m.watch(session, condition, note, timeout)
	if err != nil {
		return toolResult{text: "Couldn't watch it: " + err.Error(), isError: true}
	}
	return toolResult{
		text:      fmt.Sprintf("Watching [%s]: %s. You'll get the result as a message.", item.ID, scheduleLabel(item)),
		activity:  "Watching " + scheduleLabel(item),
		sessionID: session.ID,
	}
}

func runRenameAgent(ctx context.Context, m *Manager, input map[string]any) toolResult {
	session, err := m.resolveAgent(input)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	name, _ := input["name"].(string)
	event := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqRenameSession, SessionID: session.ID, Text: name})
	if event.Type == protocol.EvtError || event.Session == nil {
		return toolResult{text: "Couldn't rename it: " + strings.TrimPrefix(event.Error, "daemon: "), isError: true}
	}
	if event.Session.OriginalName == "" {
		return toolResult{
			text:      fmt.Sprintf("%s has its own name back.", event.Session.Name),
			activity:  "Gave " + event.Session.Name + " its own name back",
			sessionID: session.ID,
		}
	}
	return toolResult{
		text:      fmt.Sprintf("Renamed %s to %s; its id is still %s.", session.Name, event.Session.Name, session.ID),
		activity:  fmt.Sprintf("Renamed %s to %s", session.Name, event.Session.Name),
		sessionID: session.ID,
	}
}

func runListSchedules(_ context.Context, m *Manager, _ map[string]any) toolResult {
	items := m.schedules.list()
	if len(items) == 0 {
		return toolResult{text: "Nothing is scheduled.", activity: "Checked your schedule"}
	}
	var b strings.Builder
	for _, item := range items {
		fmt.Fprintf(&b, "- [%s] %s", item.ID, scheduleLabel(item))
		if item.Kind == protocol.ScheduleReminder {
			fmt.Fprintf(&b, ": %q", item.Text)
		} else if item.Text != "" {
			fmt.Fprintf(&b, " (because: %s)", item.Text)
		}
		b.WriteString("\n")
	}
	return toolResult{text: b.String(), activity: "Checked your schedule"}
}

func runUpdateSchedule(_ context.Context, m *Manager, input map[string]any) toolResult {
	id, _ := input["id"].(string)
	now := m.now()
	var change scheduleChange
	if text, ok := input["message"].(string); ok {
		change.text = &text
	}
	if minutes, ok := input["in_minutes"].(float64); ok {
		at := now.Add(time.Duration(minutes * float64(time.Minute)))
		change.at = &at
	} else if text, _ := input["at"].(string); strings.TrimSpace(text) != "" {
		at, err := parseWhen(text, now)
		if err != nil {
			return toolResult{text: err.Error(), isError: true}
		}
		change.at = &at
	}
	if minutes, ok := input["every_minutes"].(float64); ok {
		every := time.Duration(minutes * float64(time.Minute))
		change.every = &every
	}
	if value, ok := input["times"].(float64); ok {
		times := int(value)
		change.times = &times
	}
	if text, _ := input["until"].(string); strings.TrimSpace(text) != "" {
		until, err := parseWhen(text, now)
		if err != nil {
			return toolResult{text: err.Error(), isError: true}
		}
		change.until = &until
	}
	if minutes, ok := input["timeout_minutes"].(float64); ok {
		timeout := time.Duration(minutes * float64(time.Minute))
		change.timeout = &timeout
	}
	item, err := m.updateSchedule(id, change)
	if err != nil {
		return toolResult{text: "Couldn't change it: " + err.Error(), isError: true}
	}
	return toolResult{text: "Changed: " + scheduleLabel(item), activity: "Changed a schedule: " + scheduleLabel(item)}
}

func runCancelSchedule(_ context.Context, m *Manager, input map[string]any) toolResult {
	id, _ := input["id"].(string)
	item, err := m.cancelSchedule(id)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	return toolResult{text: "Cancelled.", activity: "Cancelled: " + scheduleLabel(item)}
}

// scheduleLabel says what a schedule is and when, in a line.
func scheduleLabel(item protocol.ManagerSchedule) string {
	due := time.UnixMilli(item.Due)
	if item.Kind == protocol.ScheduleWatch {
		return fmt.Sprintf("%s until %s, giving up at %s", item.AgentName, conditionText(item.Condition), due.Format("15:04"))
	}
	label := fmt.Sprintf("“%s” at %s", clip(item.Text, 80), due.Format("15:04"))
	if item.Every > 0 {
		label += ", every " + roundDuration(time.Duration(item.Every)*time.Second)
		switch {
		case item.Times > 0:
			left := item.Times - item.Fired
			label += fmt.Sprintf(", %d more", left)
		case item.Until > 0:
			label += ", until " + time.UnixMilli(item.Until).Format("15:04")
		}
	}
	return label
}

// parseWhen reads a local time: HH:MM, the next one to come, or ISO 8601.
func parseWhen(text string, now time.Time) (time.Time, error) {
	text = strings.TrimSpace(text)
	if clock, err := time.ParseInLocation("15:04", text, now.Location()); err == nil {
		at := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, now.Location())
		if !at.After(now) {
			// Tomorrow by the calendar, not 24 hours on: across a clock
			// change those differ by an hour.
			at = time.Date(now.Year(), now.Month(), now.Day()+1, clock.Hour(), clock.Minute(), 0, 0, now.Location())
		}
		return at, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04"} {
		if at, err := time.ParseInLocation(layout, text, now.Location()); err == nil {
			return at, nil
		}
	}
	return time.Time{}, fmt.Errorf("couldn't read %q as a time; use HH:MM or ISO 8601", text)
}

func capitalize(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// formatTranscript renders messages one per line or paragraph, oldest first,
// within maxToolText, keeping the newest when it must cut.
func formatTranscript(messages []protocol.Message) string {
	if len(messages) == 0 {
		return "No messages."
	}
	lines := make([]string, 0, len(messages))
	for _, message := range messages {
		when := time.UnixMilli(message.Ts).Format("15:04")
		switch {
		case message.Tool != nil:
			line := fmt.Sprintf("[%s] tool %s", when, message.Tool.Name)
			if message.Tool.Summary != "" {
				line += ": " + clip(message.Tool.Summary, 300)
			}
			if message.Tool.Status == protocol.ToolError {
				line += " (failed)"
			}
			lines = append(lines, line)
		case strings.TrimSpace(message.Text) != "":
			role := string(message.Role)
			if message.IsSidechain {
				role += " (subagent)"
			}
			lines = append(lines, fmt.Sprintf("[%s] %s: %s", when, role, clip(message.Text, maxMessageText)))
		}
	}
	total := 0
	start := len(lines)
	for start > 0 && total+len(lines[start-1]) < maxToolText {
		start--
		total += len(lines[start]) + 1
	}
	text := strings.Join(lines[start:], "\n")
	if start > 0 {
		text = "(Earlier messages left out for length.)\n" + text
	}
	return text
}

func stateText(s protocol.Session) string {
	switch s.State {
	case protocol.StateWaitingInput:
		if s.Question != nil {
			ask := s.Question.Title
			if s.Question.Detail != "" {
				ask += " " + clip(s.Question.Detail, 160)
			} else if s.Question.Prompt != "" {
				ask += " " + clip(s.Question.Prompt, 160)
			}
			return "needs the user: " + strings.Join(strings.Fields(ask), " ")
		}
		return "needs the user"
	case protocol.StateBusy:
		return "working"
	case protocol.StateIdle:
		return "idle"
	default:
		return string(s.State)
	}
}

func shortPath(path string) string {
	if home := homeDir(); home != "" && strings.HasPrefix(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func ago(now time.Time, ms int64) string {
	if ms <= 0 {
		return "a while ago"
	}
	d := now.Sub(time.UnixMilli(ms))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func clip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut] + "…"
}
