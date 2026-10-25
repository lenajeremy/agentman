package manager

import (
	"context"
	"fmt"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Words the user types on a card are for Agentman, not the agent: the card
// is answered, the user's reply joins the conversation, and Agentman takes a
// turn to act on it, passing on what the agent needs or holding off as the
// user says. A tapped option still answers the agent at once.

// cardReply is the card a turn answers, for the question it may answer.
type cardReply struct {
	sessionID  string
	questionID string
}

type cardReplyKey struct{}

func withCardReply(ctx context.Context, reply cardReply) context.Context {
	return context.WithValue(ctx, cardReplyKey{}, reply)
}

// repliedTo reports whether the turn answers the user's reply on this
// question's card: the user's words are their OK for it.
func repliedTo(ctx context.Context, sessionID, questionID string) bool {
	reply, ok := ctx.Value(cardReplyKey{}).(cardReply)
	return ok && questionID != "" && reply.sessionID == sessionID && reply.questionID == questionID
}

// typedReply is what the user wrote on a card for Agentman: a reply on a
// card that asks for one, or a typed answer with no option chosen.
func typedReply(card *protocol.ManagerCard, req protocol.Request) string {
	switch card.Kind {
	case protocol.CardNeedsYou:
		return strings.TrimSpace(req.Text)
	case protocol.CardQuestion:
		if req.OptionKey != "" || len(req.OptionKeys) > 0 {
			return ""
		}
		return strings.TrimSpace(req.AnswerText + req.Text)
	}
	return ""
}

// replyThroughAgentmanLocked answers a card with the user's words, and has
// Agentman act on them. The caller holds m.cardMu.
func (m *Manager) replyThroughAgentmanLocked(ctx context.Context, entry protocol.ManagerEntry, text string) protocol.Event {
	if _, err := m.model(ctx); err != nil {
		return protocol.Event{Type: protocol.EvtError, Error: "Add your " + providerName(providerOf(m.currentSettings().ChatModel)) +
			" API key in Settings first, so Agentman can act on your reply."}
	}
	card := entry.Card
	closed := m.closeCardLocked(entry, protocol.CardAnswered, "You replied to Agentman")
	m.addEntry(protocol.ManagerEntry{Role: "user", Text: text, SessionID: card.SessionID})
	m.wakeFirst(wakeup{
		user:   true,
		prompt: cardReplyPrompt(card, text),
		reply:  &cardReply{sessionID: card.SessionID, questionID: card.QuestionID},
	})
	return protocol.Event{Type: protocol.EvtManagerEntry, Entry: &closed}
}

// cardReplyPrompt is the user's reply as Agentman reads it: which card it
// answers, what to do with it, then their words.
func cardReplyPrompt(card *protocol.ManagerCard, text string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<card-reply agent=%q card=%q>\n", card.AgentName, card.ID)
	fmt.Fprintf(&b, "The user replied to you on %s's card, not to the agent. The card: %s", card.AgentName, card.Title)
	if card.Detail != "" {
		fmt.Fprintf(&b, " (%s)", clip(card.Detail, 300))
	}
	b.WriteString(".\n")
	if card.Kind == protocol.CardQuestion {
		fmt.Fprintf(&b, "%s's question is still waiting. If their words answer it, answer it with answer_question, "+
			"choosing the option they mean; their reply is their OK. If they're unclear, ask them.\n", card.AgentName)
	}
	fmt.Fprintf(&b, "Act on their reply for them: pass on what %s needs with send_to_agent, in your words or theirs, "+
		"or hold off as they say. Then tell them in a line what you did.\n</card-reply>\n", card.AgentName)
	b.WriteString(text)
	return b.String()
}

// wakeFirst queues a turn ahead of Agentman's own reminders and watches, and
// takes it now if nothing is being written.
func (m *Manager) wakeFirst(w wakeup) {
	m.mu.Lock()
	m.wakeups = append([]wakeup{w}, m.wakeups...)
	m.mu.Unlock()
	m.startWakeup()
}
