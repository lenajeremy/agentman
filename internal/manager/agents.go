package manager

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lenajeremy/agentman/internal/knowledge"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// What agents can ask Agentman, through am mcp: the user's shared notes,
// the keys it keeps, and a word in the conversation. Agents never see the
// private notes or a key's value.

// handleAgent answers one agent_* request.
func (m *Manager) handleAgent(ctx context.Context, req protocol.Request) protocol.Event {
	reply := func(text string) protocol.Event {
		return protocol.Event{Type: protocol.EvtAgentReply, Preview: text}
	}
	fail := func(err error) protocol.Event {
		return protocol.Event{Type: protocol.EvtError, Error: err.Error()}
	}
	if !m.active() {
		return fail(errors.New("Agentman isn't set up on this Mac yet"))
	}
	session := m.agentAt(ctx, req.Port, req.Path)

	switch req.Type {
	case protocol.ReqAgentKnowledgeSearch:
		hits, err := m.knowledge.Search(req.Text, knowledge.Shared, 8)
		if err != nil {
			return fail(err)
		}
		if len(hits) == 0 {
			return reply("No shared notes match. The user may not have written one about this.")
		}
		var b strings.Builder
		for _, hit := range hits {
			fmt.Fprintf(&b, "## %s (%s)\n%s\n\n", hit.Title, hit.Name, hit.Snippet)
		}
		b.WriteString("Read one in full with read_note.")
		return reply(b.String())
	case protocol.ReqAgentKnowledgeRead:
		note, err := m.knowledge.Read(knowledge.Shared, strings.TrimSpace(req.Text))
		if errors.Is(err, knowledge.ErrNotFound) {
			return fail(errors.New("there is no shared note called " + req.Text))
		}
		if err != nil {
			return fail(err)
		}
		return reply("# " + note.Title + "\n\n" + note.Body)
	case protocol.ReqAgentKeys:
		keys := m.keys.list()
		if len(keys) == 0 {
			return reply("Agentman keeps no keys yet.")
		}
		var b strings.Builder
		for _, key := range keys {
			fmt.Fprintf(&b, "- %s", key.Name)
			if key.Description != "" {
				fmt.Fprintf(&b, ": %s", key.Description)
			}
			switch {
			case !key.Set:
				b.WriteString(" (no value yet)")
			case key.Rule == protocol.KeyAsk || key.Rule == protocol.KeyProjects && !inAnyFolder(session.Cwd, key.Projects):
				b.WriteString(" (the user is asked first)")
			}
			b.WriteString("\n")
		}
		b.WriteString("Ask for one with request_key; it is written into your project's .env.")
		return reply(b.String())
	case protocol.ReqAgentRequestKey:
		text, err := m.giveKey(ctx, session, req.Text, req.AnswerText)
		if err != nil {
			return fail(err)
		}
		return reply(text)
	case protocol.ReqAgentTell:
		text := strings.TrimSpace(req.Text)
		if text == "" {
			return fail(errors.New("say something"))
		}
		text = clip(text, 2000)
		if req.Approve {
			m.cardMu.Lock()
			m.postNeedsYouLocked(session, protocol.Brief{SessionID: session.ID, NeedsYou: text})
			m.cardMu.Unlock()
			return reply("The user has it, with a box to reply. Their answer comes to you as a message.")
		}
		m.addEntry(protocol.ManagerEntry{Role: "agent", Text: text, SessionID: session.ID})
		m.note(fmt.Sprintf("%s told you: %s", session.Name, text))
		return reply("Agentman has it, and the user will see it.")
	}
	return fail(errors.New("unknown agent request"))
}

// agentAt is the agent asking: the one whose process the asker descends
// from, else the one working in its folder, else an agent Agentman doesn't
// know by name, in that folder.
func (m *Manager) agentAt(ctx context.Context, pid int, cwd string) protocol.Session {
	sessions := m.agents.Sessions()
	if pid > 0 {
		if tree, err := tmux.SnapshotProcessTree(ctx); err == nil {
			for _, s := range sessions {
				if s.AgentPID > 0 && tree.OwnsPID(s.AgentPID, pid) {
					return s
				}
			}
		}
	}
	cwd = filepath.Clean(cwd)
	var best protocol.Session
	for _, s := range sessions {
		if s.Cwd != "" && insideFolder(cwd, s.Cwd) && len(s.Cwd) > len(best.Cwd) {
			best = s
		}
	}
	if best.ID != "" {
		return best
	}
	return protocol.Session{Name: "an agent in " + shortPath(cwd), Cwd: cwd}
}

// handleKnowledge answers the apps' knowledge requests.
func (m *Manager) handleKnowledge(req protocol.Request) protocol.Event {
	fail := func(err error) protocol.Event {
		if errors.Is(err, knowledge.ErrNotFound) {
			return protocol.Event{Type: protocol.EvtError, Error: "There is no such note."}
		}
		return protocol.Event{Type: protocol.EvtError, Error: strings.TrimPrefix(err.Error(), "knowledge: ")}
	}
	switch req.Type {
	case protocol.ReqKnowledgeList:
		notes, err := m.knowledge.List("")
		if err != nil {
			return fail(err)
		}
		list := make([]protocol.Note, 0, len(notes))
		for _, note := range notes {
			list = append(list, toProtocolNote(note))
		}
		return protocol.Event{Type: protocol.EvtKnowledge, Notes: list}
	case protocol.ReqKnowledgeRead:
		note, err := m.knowledge.Read(string(req.Kind), req.Path)
		if err != nil {
			return fail(err)
		}
		shown := toProtocolNote(note)
		return protocol.Event{Type: protocol.EvtKnowledge, Note: &shown}
	case protocol.ReqKnowledgeWrite:
		if req.Note == nil {
			return fail(errors.New("no note was sent"))
		}
		saved, err := m.knowledge.Write(knowledge.Note{
			Name: req.Note.Name, Tier: req.Note.Tier, Title: req.Note.Title, Always: req.Note.Always, Body: req.Note.Body,
		})
		if err != nil {
			return fail(err)
		}
		shown := toProtocolNote(saved)
		return protocol.Event{Type: protocol.EvtKnowledge, Note: &shown}
	case protocol.ReqKnowledgeDelete:
		if err := m.knowledge.Delete(string(req.Kind), req.Path); err != nil {
			return fail(err)
		}
		return m.handleKnowledge(protocol.Request{Type: protocol.ReqKnowledgeList})
	}
	return fail(errors.New("unknown knowledge request"))
}

func toProtocolNote(note knowledge.Note) protocol.Note {
	return protocol.Note{
		Name: note.Name, Tier: note.Tier, Title: note.Title, Always: note.Always,
		Body: note.Body, UpdatedAt: note.UpdatedAt,
	}
}
