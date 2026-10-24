package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

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

var toolDefs = []toolDef{
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
	var matches []protocol.Session
	for _, s := range sessions {
		if strings.EqualFold(s.Name, query) {
			matches = append(matches, s)
		}
	}
	if len(matches) == 0 {
		for _, s := range sessions {
			if strings.Contains(strings.ToLower(s.Name), strings.ToLower(query)) {
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
	var b strings.Builder
	now := time.Now()
	for _, s := range sessions {
		fmt.Fprintf(&b, "- %s (%s, %s) — %s", s.Name, s.Kind, shortPath(s.Cwd), stateText(s))
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
			return "needs the user: " + strings.TrimSpace(ask)
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
