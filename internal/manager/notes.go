package manager

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/lenajeremy/agentman/internal/knowledge"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// Notes Agentman suggests: a preference, a convention or a fact about a
// project it thinks worth remembering. Each is a card; nothing is written to
// the user's notes until they approve it, as it is or as they edited it.

const (
	maxNoteTitle = 120
	maxNoteBody  = 4000
)

func runSuggestNote(_ context.Context, m *Manager, input map[string]any) toolResult {
	title, _ := input["title"].(string)
	body, _ := input["body"].(string)
	tier, _ := input["tier"].(string)
	name, _ := input["name"].(string)
	why, _ := input["why"].(string)
	always, _ := input["always"].(bool)
	title, body = strings.TrimSpace(title), strings.TrimSpace(body)
	if title == "" || body == "" {
		return toolResult{text: "A note needs a title and a body.", isError: true}
	}
	if utf8.RuneCountInString(title) > maxNoteTitle || utf8.RuneCountInString(body) > maxNoteBody {
		return toolResult{text: "Keep a note short: a title in a line and a body of a few paragraphs at most.", isError: true}
	}
	if tier != knowledge.Private {
		tier = knowledge.Shared
	}

	m.cardMu.Lock()
	defer m.cardMu.Unlock()
	for _, open := range m.conversation.openCards() {
		if open.Card.Kind == protocol.CardNote && open.Card.Note != nil && strings.EqualFold(open.Card.Note.Title, title) {
			return toolResult{text: "That note is already waiting for the user to decide."}
		}
	}
	if m.knowledge != nil && name == "" {
		for _, t := range []string{knowledge.Shared, knowledge.Private} {
			notes, _ := m.knowledge.List(t)
			for _, note := range notes {
				if strings.EqualFold(note.Title, title) {
					return toolResult{text: fmt.Sprintf("A note called %q is already kept, as %s. Pass its name to suggest a change to it.", note.Title, note.Name)}
				}
			}
		}
	}

	id := newID()
	note := &protocol.Note{Name: strings.TrimSpace(name), Tier: tier, Title: title, Body: body, Always: always}
	card := &protocol.ManagerCard{
		ID: id, Kind: protocol.CardNote, State: protocol.CardOpen,
		AgentName: "Agentman", Title: "suggests a note", Detail: body, Reason: strings.TrimSpace(why), Note: note,
	}
	text := fmt.Sprintf("Suggested a note: “%s”", title)
	m.addEntry(protocol.ManagerEntry{ID: id, Role: "card", Text: text, Card: card})
	// No activity line: the card says it.
	return toolResult{text: "Suggested. The user decides: nothing is saved unless they approve it, and they may edit it first."}
}

// answerNoteCardLocked saves a suggested note the user approved, with their
// edits when they made any. m.cardMu is held.
func (m *Manager) answerNoteCardLocked(entry protocol.ManagerEntry, req protocol.Request) protocol.Event {
	card := entry.Card
	if card.Note == nil || m.knowledge == nil {
		return protocol.Event{Type: protocol.EvtError, Error: "That note can't be saved."}
	}
	note := *card.Note
	edited := false
	if req.Note != nil {
		if title := strings.TrimSpace(req.Note.Title); title != "" && title != note.Title {
			note.Title, edited = title, true
		}
		if body := strings.TrimSpace(req.Note.Body); body != "" && body != note.Body {
			note.Body, edited = body, true
		}
		if req.Note.Tier == knowledge.Private || req.Note.Tier == knowledge.Shared {
			edited = edited || req.Note.Tier != note.Tier
			note.Tier = req.Note.Tier
		}
		edited = edited || req.Note.Always != note.Always
		note.Always = req.Note.Always
	}
	saved, err := m.knowledge.Write(knowledge.Note{Name: note.Name, Tier: note.Tier, Title: note.Title, Body: note.Body, Always: note.Always})
	if err != nil {
		return protocol.Event{Type: protocol.EvtError, Error: "Couldn't save it: " + strings.TrimPrefix(err.Error(), "knowledge: ")}
	}
	where := "shared with your agents"
	if saved.Tier == knowledge.Private {
		where = "kept private"
	}
	outcome := "Saved, " + where
	if edited {
		outcome = "Saved with your changes, " + where
	}
	card.Note = &protocol.Note{Name: saved.Name, Tier: saved.Tier, Title: saved.Title, Body: saved.Body, Always: saved.Always}
	closed := m.closeCardLocked(entry, protocol.CardAnswered, outcome)
	m.note(fmt.Sprintf("The user saved the note you suggested as %s (%s) [card %s]%s.", saved.Name, saved.Tier, card.ID,
		map[bool]string{true: ", with their changes", false: ""}[edited]))
	return protocol.Event{Type: protocol.EvtManagerEntry, Entry: &closed}
}
