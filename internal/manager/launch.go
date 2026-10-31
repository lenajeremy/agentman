package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Agentman starting and ending agents, and handing one agent's work to
// another: "start a Codex agent in ~/api to add rate limiting", "have Codex
// continue what Claude was doing". A started agent runs in a terminal as
// `am codex` would start it, so the user keeps its live view, and Agentman
// watches it until its turn ends.

// launchKinds are the agents start_agent can start, by the name the model
// uses for each.
var launchKinds = map[string]protocol.Kind{
	"claude":      protocol.KindClaude,
	"codex":       protocol.KindCodex,
	"cursor":      protocol.KindCursorCLI,
	"opencode":    protocol.KindOpenCode,
	"kiro":        protocol.KindKiro,
	"antigravity": protocol.KindAntigravity,
}

var launchKindNames = []string{"claude", "codex", "cursor", "opencode", "kiro", "antigravity"}

// Bounds on a handoff, so the new agent's first message stays readable.
const (
	handoffRequest = 800
	handoffLast    = 1500
	handoffFiles   = 20
)

var continueFromProperty = map[string]any{
	"type": "string",
	"description": "An agent whose work this one carries on: its goal, what it did, where it stopped, the user's last request to it, " +
		"its last message and the files it changed go along with the task. Its id from list_agents, or its name.",
}

func runStartAgent(ctx context.Context, m *Manager, input map[string]any) toolResult {
	name, _ := input["kind"].(string)
	kind, ok := launchKinds[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return toolResult{text: "Say which agent to start: " + strings.Join(launchKindNames, ", ") + ".", isError: true}
	}
	where, _ := input["folder"].(string)
	folder, rel, err := m.launchFolder(where)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	task, _ := input["task"].(string)
	task = strings.TrimSpace(task)
	if task == "" {
		return toolResult{text: "Say what the new agent should do.", isError: true}
	}
	text := task
	var from protocol.Session
	if source, _ := input["continue_from"].(string); strings.TrimSpace(source) != "" {
		if from, err = m.resolveAgent(map[string]any{"agent": source}); err != nil {
			return toolResult{text: err.Error(), isError: true}
		}
		text = task + "\n\n" + m.handoff(ctx, from)
	}

	event := m.agents.Request(ctx, protocol.Request{
		Type: protocol.ReqStartSession, Kind: kind, Path: rel, Text: text, ClientID: "agentman-" + newID(),
	})
	if event.Type == protocol.EvtError || event.SessionID == "" {
		return toolResult{text: "Couldn't start it: " + plainError(event.Error), isError: true}
	}
	label := agentLabel(kind)
	place := "~/" + rel
	session := protocol.Session{ID: event.SessionID, Kind: kind, Name: label + " in " + filepath.Base(folder), Cwd: folder}

	result := fmt.Sprintf("Started %s in %s; its id is %s. It shows up in list_agents once it has registered, within a few seconds.", label, place, session.ID)
	if watch, set := input["watch"].(bool); !set || watch {
		if item, err := m.watch(session, protocol.WatchTurnEnd, "You started it to: "+clip(task, 300), 0); err == nil {
			result += fmt.Sprintf(" Watching it [%s] until its turn ends.", item.ID)
		}
	}
	activity := fmt.Sprintf("Started %s in %s: “%s”", label, place, clip(task, 120))
	if from.ID != "" {
		activity = fmt.Sprintf("Started %s in %s to carry on %s's work: “%s”", label, place, from.Name, clip(task, 100))
	}
	return toolResult{text: result, activity: activity, sessionID: session.ID}
}

func runEndAgent(ctx context.Context, m *Manager, input map[string]any) toolResult {
	session, err := m.resolveAgent(input)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	if session.State == protocol.StateEnded {
		return toolResult{text: session.Name + " has already ended."}
	}
	event := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqEndSession, SessionID: session.ID})
	if event.Type == protocol.EvtError {
		problem := plainError(event.Error)
		if strings.Contains(problem, "pane Agentman started") || strings.Contains(problem, "do not run in a pane") {
			problem = session.Name + " is running in the user's own terminal, not one Agentman started; they close it there"
		}
		return toolResult{text: "Couldn't end it: " + problem + ".", isError: true}
	}
	return toolResult{
		text:      fmt.Sprintf("Ended %s and closed its terminal. Its conversation is kept; reopen_agent brings it back.", session.Name),
		activity:  "Ended " + session.Name,
		sessionID: session.ID,
	}
}

func runReopenAgent(ctx context.Context, m *Manager, input map[string]any) toolResult {
	session, err := m.resolveAgent(input)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	event := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqResumeSession, SessionID: session.ID})
	if event.Type == protocol.EvtError || event.SessionID == "" {
		return toolResult{text: "Couldn't reopen it: " + plainError(event.Error), isError: true}
	}
	text := fmt.Sprintf("Reopened %s in a terminal of its own.", session.Name)
	if event.SessionID != session.ID {
		text += " Its id is now " + event.SessionID + "."
	}
	return toolResult{text: text, activity: "Reopened " + session.Name, sessionID: event.SessionID}
}

// launchFolder reads where to start an agent: "~/api", "/Users/me/api", or
// an agent's name or id for its folder. Agents start inside the home
// folder, as the daemon takes it: a path relative to home.
func (m *Manager) launchFolder(raw string) (folder, rel string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("say which folder to start it in")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	switch {
	case raw == "~":
		folder = home
	case strings.HasPrefix(raw, "~/"):
		folder = filepath.Join(home, raw[2:])
	case filepath.IsAbs(raw):
		folder = filepath.Clean(raw)
	default:
		if session, err := m.resolveAgent(map[string]any{"agent": raw}); err == nil && session.Cwd != "" {
			folder = session.Cwd
		} else {
			folder = filepath.Join(home, raw)
		}
	}
	rel, err = filepath.Rel(home, folder)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", "", fmt.Errorf("agents start in a folder inside %s, not %s", shortPath(home), folder)
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("there's no folder at %s", "~/"+filepath.ToSlash(rel))
	}
	return folder, filepath.ToSlash(rel), nil
}

// handoff is what an agent needs to carry on another's work: Agentman's
// brief on the first one, the user's last request to it, its last message,
// and the files it left changed. The receiving agent is told to check the
// files rather than trust the summary.
func (m *Manager) handoff(ctx context.Context, from protocol.Session) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You're carrying on work that another agent, %s (%s), was doing in %s.", from.Name, agentLabel(from.Kind), shortPath(from.Cwd))
	if brief, ok := m.briefs.get(from.ID); ok {
		if brief.Goal != "" {
			fmt.Fprintf(&b, "\nIts goal: %s", brief.Goal)
		}
		if len(brief.Done) > 0 {
			b.WriteString("\nWhat it did:")
			for _, done := range brief.Done {
				fmt.Fprintf(&b, "\n- %s", done)
			}
		}
		if brief.Now != "" {
			fmt.Fprintf(&b, "\nWhere it was: %s", brief.Now)
		}
		if brief.Blocked != "" {
			fmt.Fprintf(&b, "\nWhat stopped it: %s", brief.Blocked)
		}
		if brief.NeedsYou != "" {
			fmt.Fprintf(&b, "\nIt was waiting on the user for: %s", brief.NeedsYou)
		}
	}

	page := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqFetchMessages, SessionID: from.ID, Limit: 30})
	if page.Page != nil {
		var asked, said string
		for _, message := range page.Page.Messages {
			text := strings.TrimSpace(message.Text)
			if text == "" || message.Tool != nil {
				continue
			}
			switch message.Role {
			case protocol.RoleUser:
				asked = text
			case protocol.RoleAssistant:
				said = text
			}
		}
		if asked != "" {
			fmt.Fprintf(&b, "\nThe user's last request to it: “%s”", clip(asked, handoffRequest))
		}
		if said != "" {
			fmt.Fprintf(&b, "\nIts last message: “%s”", clip(said, handoffLast))
		}
	}

	changes := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqListChanges, SessionID: from.ID})
	if changes.Workspace != nil && len(changes.Workspace.Changes) > 0 {
		list := changes.Workspace.Changes
		fmt.Fprintf(&b, "\nUncommitted changes in %s:", shortPath(from.Cwd))
		for i, change := range list {
			if i == handoffFiles {
				fmt.Fprintf(&b, "\n- and %d more", len(list)-handoffFiles)
				break
			}
			fmt.Fprintf(&b, "\n- %s (+%d −%d)", change.Path, change.Added, change.Removed)
		}
	}
	b.WriteString("\nThis is a summary: read the files and the state of the work yourself before relying on it.")
	return printable(b.String())
}

// printable drops control characters other than line breaks and tabs: an
// agent's text goes into a terminal, where they would act as keys.
func printable(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, text)
}

func agentLabel(kind protocol.Kind) string {
	switch kind {
	case protocol.KindClaude:
		return "Claude"
	case protocol.KindCodex:
		return "Codex"
	case protocol.KindCursor, protocol.KindCursorCLI:
		return "Cursor"
	case protocol.KindOpenCode:
		return "OpenCode"
	case protocol.KindKiro:
		return "Kiro"
	case protocol.KindAntigravity:
		return "Antigravity"
	}
	return string(kind)
}

// plainError is a daemon error as the model should read it.
func plainError(message string) string {
	for _, prefix := range []string{"daemon: ", "source: ", "workspace: "} {
		message = strings.TrimPrefix(message, prefix)
	}
	if message == "" {
		return "the daemon gave no reason"
	}
	return message
}
