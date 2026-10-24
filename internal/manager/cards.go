package manager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Cards are the decisions Agentman puts in front of the user: an agent's
// question it may not answer itself, or an agent waiting on the user for
// something in words. A card stays only while it needs the user. Answered on
// the card, it shrinks to a line saying what was chosen. Answered anywhere
// else (in the terminal, in the agent's own thread, from the phone) or no
// longer needed, it is settled, and the apps take it away.

// startupCheck is how long after starting Agentman checks the cards it kept
// against the agents as they are now: long enough for the first sweep.
const startupCheck = 5 * time.Second

// active reports whether Agentman is set up. Without a key it does nothing
// at all, so upgrading am changes nothing until the user turns it on.
func (m *Manager) active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.keySet
}

// takeQuestion deals with a question an agent has just asked: approved, when
// the policy allows it and the user lets Agentman, or put in front of the
// user as a card.
func (m *Manager) takeQuestion(ctx context.Context, session protocol.Session) {
	q := session.Question
	if q == nil || !m.active() {
		return
	}
	m.cardMu.Lock()
	defer m.cardMu.Unlock()
	if m.openCardLocked(session.ID, q.ID) != nil {
		return
	}
	d := classify(session, q)
	if d.auto && m.currentSettings().AutoApprove {
		err := m.answer(ctx, session.ID, protocol.Request{QuestionID: q.ID, OptionKey: d.optionKey})
		if err == nil {
			text := fmt.Sprintf("Approved for %s: %s", session.Name, d.summary)
			m.addEntry(protocol.ManagerEntry{Role: "activity", Text: text, Tool: "approve", SessionID: session.ID})
			m.note(text)
			return
		}
		d.reason = "approving it failed: " + err.Error()
	}
	m.postQuestionCardLocked(session, d, "")
}

// answer answers an agent's question through the daemon.
func (m *Manager) answer(ctx context.Context, sessionID string, req protocol.Request) error {
	req.Type = protocol.ReqAnswer
	req.SessionID = sessionID
	req.ClientID = "agentman-" + newID()
	event := m.agents.Request(ctx, req)
	if event.Type == protocol.EvtError || event.Status == protocol.StatusFailed {
		if event.Error == "" {
			return errors.New("the agent didn't take it")
		}
		return errors.New(strings.TrimPrefix(event.Error, "daemon: "))
	}
	return nil
}

// postQuestionCardLocked puts an agent's question in front of the user, with
// Agentman's pick marked when it has one. One question card per agent: a
// newer question settles the older card.
func (m *Manager) postQuestionCardLocked(session protocol.Session, d decision, suggested string) protocol.ManagerEntry {
	q := session.Question
	for _, open := range m.conversation.openCards() {
		if open.Card.SessionID != session.ID || open.Card.Kind != protocol.CardQuestion {
			continue
		}
		if open.Card.QuestionID == q.ID {
			if suggested != "" && open.Card.Suggested != suggested {
				open.Card.Suggested = suggested
				return m.addEntry(open)
			}
			return open
		}
		m.settleCardLocked(open, "The agent moved on")
	}
	title, detail := cardWording(q, d)
	id := newID()
	card := &protocol.ManagerCard{
		ID: id, Kind: protocol.CardQuestion, State: protocol.CardOpen,
		SessionID: session.ID, AgentName: session.Name,
		Title: title, Detail: detail, Reason: d.reason,
		QuestionID: q.ID, Options: q.Options, Multiple: q.Multiple, Custom: q.Custom,
		Suggested: suggested,
	}
	if brief, ok := m.briefs.get(session.ID); ok {
		card.Context = brief.Summary
	}
	text := session.Name + " " + title
	if d.reason != "" {
		text += " (waits for you: " + d.reason + ")"
	}
	entry := m.addEntry(protocol.ManagerEntry{ID: id, Role: "card", Text: text, SessionID: session.ID, Card: card})
	m.note(fmt.Sprintf("Put a card in front of the user [card %s]: %s", id, text))
	return entry
}

// cardWording is a question's card title and detail: what is asked, in a
// line, and the command, file or question in full.
func cardWording(q *protocol.Question, d decision) (title, detail string) {
	switch d.kind {
	case "command":
		return "wants to run a command", d.target
	case "edit":
		return "wants to " + d.summary, strings.TrimSpace(q.Detail)
	case "read":
		return "wants to " + d.summary, d.target
	case "network":
		return "wants to open a link", d.target
	case "continue":
		return "asks whether to carry on", ""
	}
	title = strings.TrimSpace(q.Prompt)
	if title == "" {
		title = strings.TrimSpace(q.Title)
	}
	var parts []string
	for _, part := range []string{q.Title, q.Detail} {
		if part = strings.TrimSpace(part); part != "" && part != title {
			parts = append(parts, part)
		}
	}
	return "asks: " + title, strings.Join(parts, "\n\n")
}

// postNeedsYouLocked puts what a brief says an agent needs in front of the
// user, with a box to reply, unless the same need is already there.
func (m *Manager) postNeedsYouLocked(session protocol.Session, brief protocol.Brief) {
	for _, open := range m.conversation.openCards() {
		if open.Card.SessionID != session.ID || open.Card.Kind != protocol.CardNeedsYou {
			continue
		}
		if open.Card.Title == brief.NeedsYou {
			return
		}
		m.settleCardLocked(open, "Replaced by a newer need")
	}
	id := newID()
	card := &protocol.ManagerCard{
		ID: id, Kind: protocol.CardNeedsYou, State: protocol.CardOpen,
		SessionID: session.ID, AgentName: session.Name,
		Title: brief.NeedsYou, Context: brief.Summary,
	}
	text := session.Name + " needs you: " + brief.NeedsYou
	m.addEntry(protocol.ManagerEntry{ID: id, Role: "card", Text: text, SessionID: session.ID, Card: card})
	m.note(fmt.Sprintf("Put a card in front of the user [card %s]: %s", id, text))
}

// settleNeedsYouLocked takes away an agent's needs-you card, if it has one.
func (m *Manager) settleNeedsYouLocked(sessionID, outcome string) {
	for _, open := range m.conversation.openCards() {
		if open.Card.SessionID == sessionID && open.Card.Kind == protocol.CardNeedsYou {
			m.settleCardLocked(open, outcome)
		}
	}
}

func (m *Manager) openCardLocked(sessionID, questionID string) *protocol.ManagerEntry {
	for _, open := range m.conversation.openCards() {
		if open.Card.SessionID == sessionID && open.Card.Kind == protocol.CardQuestion && open.Card.QuestionID == questionID {
			return &open
		}
	}
	return nil
}

func (m *Manager) cardByIDLocked(id string) (protocol.ManagerEntry, bool) {
	for _, open := range m.conversation.openCards() {
		if open.Card.ID == id {
			return open, true
		}
	}
	return protocol.ManagerEntry{}, false
}

// settleCardLocked takes a card away: it no longer needs the user.
func (m *Manager) settleCardLocked(entry protocol.ManagerEntry, outcome string) {
	m.closeCardLocked(entry, protocol.CardSettled, outcome)
	m.note(fmt.Sprintf("Took away the card about %s [card %s]: %s", entry.Card.AgentName, entry.Card.ID, outcome))
}

func (m *Manager) closeCardLocked(entry protocol.ManagerEntry, state, outcome string) protocol.ManagerEntry {
	card := *entry.Card
	card.State, card.Outcome = state, outcome
	entry.Card = &card
	return m.addEntry(entry)
}

// replyToCard is the user answering a card.
func (m *Manager) replyToCard(ctx context.Context, req protocol.Request) protocol.Event {
	m.cardMu.Lock()
	defer m.cardMu.Unlock()
	entry, ok := m.cardByIDLocked(req.CardID)
	if !ok {
		return protocol.Event{Type: protocol.EvtError, Error: "That card has already been dealt with."}
	}
	card := entry.Card
	// A suggested note is about no agent.
	if card.Kind == protocol.CardNote {
		return m.answerNoteCardLocked(entry, req)
	}
	session, live := m.session(card.SessionID)
	if !live {
		m.settleCardLocked(entry, "The agent ended")
		return protocol.Event{Type: protocol.EvtError, Error: card.AgentName + " has ended."}
	}

	var outcome string
	switch card.Kind {
	case protocol.CardQuestion:
		if session.Question == nil || session.Question.ID != card.QuestionID {
			m.settleCardLocked(entry, "Answered in the agent")
			return protocol.Event{Type: protocol.EvtError, Error: "That was already answered in " + card.AgentName + "."}
		}
		answer := protocol.Request{QuestionID: card.QuestionID, OptionKey: req.OptionKey, OptionKeys: req.OptionKeys, AnswerText: req.AnswerText}
		if err := m.answer(ctx, card.SessionID, answer); err != nil {
			return protocol.Event{Type: protocol.EvtError, Error: "Couldn't answer " + card.AgentName + ": " + err.Error()}
		}
		outcome = "You chose " + chosenText(card.Options, req)
	case protocol.CardKey:
		result, err := m.answerKeyCardLocked(ctx, session, card, req.OptionKey)
		if err != nil {
			return protocol.Event{Type: protocol.EvtError, Error: "Couldn't do that: " + err.Error()}
		}
		outcome = result
	case protocol.CardNeedsYou:
		text := strings.TrimSpace(req.Text)
		if text == "" {
			return protocol.Event{Type: protocol.EvtError, Error: "Write a reply first."}
		}
		status, err := m.sendTo(ctx, session, text)
		if err != nil {
			return protocol.Event{Type: protocol.EvtError, Error: "Couldn't reach " + card.AgentName + ": " + err.Error()}
		}
		outcome = "You replied: “" + clip(text, 120) + "”"
		if status == protocol.StatusQueued {
			outcome += " It reaches the agent when its turn ends."
		}
	default:
		return protocol.Event{Type: protocol.EvtError, Error: "That card can't be answered."}
	}
	closed := m.closeCardLocked(entry, protocol.CardAnswered, outcome)
	m.note(fmt.Sprintf("The user answered the card about %s [card %s]: %s", card.AgentName, card.ID, outcome))
	return protocol.Event{Type: protocol.EvtManagerEntry, Entry: &closed}
}

// chosenText says what an answer chose, by the options' labels.
func chosenText(options []protocol.QuestionOption, req protocol.Request) string {
	label := func(key string) string {
		for _, option := range options {
			if option.Key == key {
				return "“" + option.Label + "”"
			}
		}
		return "“" + key + "”"
	}
	var chosen []string
	if req.OptionKey != "" {
		chosen = append(chosen, label(req.OptionKey))
	}
	for _, key := range req.OptionKeys {
		chosen = append(chosen, label(key))
	}
	if text := strings.TrimSpace(req.AnswerText); text != "" {
		chosen = append(chosen, "“"+clip(text, 120)+"”")
	}
	if len(chosen) == 0 {
		return "an answer"
	}
	return strings.Join(chosen, ", ")
}

// dismissCard is the user putting a card away unanswered.
func (m *Manager) dismissCard(id string) protocol.Event {
	m.cardMu.Lock()
	defer m.cardMu.Unlock()
	entry, ok := m.cardByIDLocked(id)
	if !ok {
		return protocol.Event{Type: protocol.EvtError, Error: "That card has already been dealt with."}
	}
	if entry.Card.Kind == protocol.CardNote {
		closed := m.closeCardLocked(entry, protocol.CardDismissed, "Not saved")
		title := ""
		if entry.Card.Note != nil {
			title = entry.Card.Note.Title
		}
		m.note(fmt.Sprintf("The user dismissed the note you suggested, “%s” [card %s]. Nothing was saved; don't suggest it again.", title, id))
		return protocol.Event{Type: protocol.EvtManagerEntry, Entry: &closed}
	}
	closed := m.closeCardLocked(entry, protocol.CardDismissed, "Put away")
	m.note(fmt.Sprintf("The user put away the card about %s [card %s] without answering it here.", entry.Card.AgentName, id))
	return protocol.Event{Type: protocol.EvtManagerEntry, Entry: &closed}
}

// SessionsChanged hears the session list after a sweep that changed it. The
// check runs on Agentman's own goroutine; a newer list replaces one not yet
// checked.
func (m *Manager) SessionsChanged(sessions []protocol.Session) {
	m.sessionsMu.Lock()
	m.latestSessions = sessions
	m.sessionsMu.Unlock()
	select {
	case m.sessionsChanged <- struct{}{}:
	default:
	}
}

// watchSessions checks the open cards against the agents whenever they
// change, and once at the start for the cards kept from before a restart.
func (m *Manager) watchSessions(ctx context.Context) {
	start := time.NewTimer(startupCheck)
	defer start.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-start.C:
			m.reconcile(ctx, m.agents.Sessions())
		case <-m.sessionsChanged:
			m.sessionsMu.Lock()
			sessions := m.latestSessions
			m.sessionsMu.Unlock()
			m.reconcile(ctx, sessions)
		}
	}
}

// reconcile takes away the cards that no longer need the user: a question
// answered in the agent itself, an agent that ended, or one the user has
// written to directly since its card went up.
func (m *Manager) reconcile(ctx context.Context, sessions []protocol.Session) {
	byID := make(map[string]protocol.Session, len(sessions))
	for _, s := range sessions {
		byID[s.ID] = s
	}
	m.cardMu.Lock()
	var pickedUp []protocol.ManagerEntry
	for _, entry := range m.conversation.openCards() {
		card := entry.Card
		// A suggested note waits for the user, whatever the agents do.
		if card.Kind == protocol.CardNote {
			continue
		}
		s, live := byID[card.SessionID]
		switch {
		case !live:
			m.settleCardLocked(entry, "The agent ended")
		case card.Kind == protocol.CardQuestion:
			if s.Question == nil || s.Question.ID != card.QuestionID {
				m.settleCardLocked(entry, "Answered in the agent")
				// Should the question come back, it is taken afresh.
				m.briefMu.Lock()
				if m.asked[s.ID] == card.QuestionID {
					delete(m.asked, s.ID)
				}
				m.briefMu.Unlock()
			}
		case card.Kind == protocol.CardNeedsYou:
			if s.State == protocol.StateBusy && m.lastState[s.ID] != protocol.StateBusy {
				pickedUp = append(pickedUp, entry)
			}
		}
	}
	m.lastState = make(map[string]protocol.State, len(sessions))
	for _, s := range sessions {
		m.lastState[s.ID] = s.State
	}
	m.cardMu.Unlock()
	m.watchSessionList(sessions)

	// An agent waiting on the user got going again: if the user wrote to it
	// since the card went up, they have answered it themselves.
	for _, entry := range pickedUp {
		if !m.userWroteSince(ctx, entry.Card.SessionID, entry.Ts) {
			continue
		}
		m.cardMu.Lock()
		if current, ok := m.cardByIDLocked(entry.Card.ID); ok {
			m.settleCardLocked(current, "You replied in the agent")
		}
		m.cardMu.Unlock()
	}
}

// userWroteSince reports whether the agent's conversation has a message from
// its user after ts.
func (m *Manager) userWroteSince(ctx context.Context, sessionID string, ts int64) bool {
	page := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqFetchMessages, SessionID: sessionID, Limit: 10}).Page
	if page == nil {
		return false
	}
	for _, message := range page.Messages {
		if message.Role == protocol.RoleUser && !message.IsSidechain && message.Ts > ts && strings.TrimSpace(message.Text) != "" {
			return true
		}
	}
	return false
}

func (m *Manager) session(id string) (protocol.Session, bool) {
	for _, s := range m.agents.Sessions() {
		if s.ID == id {
			return s, true
		}
	}
	return protocol.Session{}, false
}

// sendTo sends text to an agent as its user, and says whether it was typed
// in or queued for its next turn end.
func (m *Manager) sendTo(ctx context.Context, session protocol.Session, text string) (protocol.SendStatus, error) {
	if session.Question != nil {
		return "", errors.New(session.Name + " is asking a question; answer that first")
	}
	event := m.agents.Request(ctx, protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: session.ID, Text: text, ClientID: "agentman-" + newID(),
	})
	if event.Type == protocol.EvtError || event.Status == protocol.StatusFailed {
		message := strings.TrimPrefix(event.Error, "daemon: ")
		if message == "" {
			message = "the agent didn't take it"
		}
		return "", errors.New(message)
	}
	if event.Status == "" {
		return protocol.StatusDelivered, nil
	}
	return event.Status, nil
}

// note keeps something that happened outside a reply, for the model to read
// with the user's next message.
func (m *Manager) note(text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notes = append(m.notes, time.Now().Format("15:04")+" "+text)
	if len(m.notes) > maxNotes {
		m.notes = m.notes[len(m.notes)-maxNotes:]
	}
}

// maxNotes bounds what is carried into the next message.
const maxNotes = 40

// takeNotes hands over what happened since the user last wrote, as a block
// that goes before their message.
func (m *Manager) takeNotes() string {
	m.mu.Lock()
	notes := m.notes
	m.notes = nil
	m.mu.Unlock()
	if len(notes) == 0 {
		return ""
	}
	return "<events since the user last wrote>\n- " + strings.Join(notes, "\n- ") + "\n</events>"
}
