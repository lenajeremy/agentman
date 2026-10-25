package manager

import (
	"context"
	"fmt"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// While a card waits for the user, Agentman's own reminders and watches
// report and don't act: a wake-up turn can't message an agent, answer its
// question or take a card away, so nothing pushes past what the user hasn't
// decided yet. The user's own messages are never held back.

type autonomousTurn struct{}

// withAutonomous marks a turn as one Agentman started itself, from a
// reminder or a watch, rather than from the user.
func withAutonomous(ctx context.Context) context.Context {
	return context.WithValue(ctx, autonomousTurn{}, true)
}

func isAutonomous(ctx context.Context) bool {
	autonomous, _ := ctx.Value(autonomousTurn{}).(bool)
	return autonomous
}

// pausingTools are the ones that would move past a card.
var pausingTools = map[string]bool{"send_to_agent": true, "answer_question": true, "dismiss_card": true}

// pausingCards are the open cards an agent is waiting behind: a question,
// a request for the user, or a key. A suggested note holds nothing up.
func (m *Manager) pausingCards() []protocol.ManagerEntry {
	var cards []protocol.ManagerEntry
	for _, entry := range m.conversation.openCards() {
		if entry.Card.Kind != protocol.CardNote {
			cards = append(cards, entry)
		}
	}
	return cards
}

// describeCards names the cards, a few of them, for the model.
func describeCards(cards []protocol.ManagerEntry) string {
	const shown = 3
	parts := make([]string, 0, shown+1)
	for i, entry := range cards {
		if i == shown {
			parts = append(parts, fmt.Sprintf("and %d more", len(cards)-shown))
			break
		}
		parts = append(parts, entry.Card.AgentName+": "+entry.Card.Title)
	}
	return strings.Join(parts, "; ")
}

func pausedResult(cards []protocol.ManagerEntry) toolResult {
	return toolResult{isError: true, text: "Paused: a card is open for the user (" + describeCards(cards) + "). " +
		"This turn is your own reminder or watch, not the user, so it can't message agents, answer their questions " +
		"or dismiss cards until the user responds. Tell the user what happened instead."}
}

// withPauseNote tells a wake-up turn, inside its own tag, that the flow is
// paused, when a card is open as it starts.
func (m *Manager) withPauseNote(prompt string) string {
	cards := m.pausingCards()
	if len(cards) == 0 {
		return prompt
	}
	note := "A card is open for the user (" + describeCards(cards) + "), so the flow is paused until they respond: " +
		"in this turn, don't message agents, answer their questions or dismiss cards. Tell the user what happened instead."
	if i := strings.LastIndex(prompt, "\n</"); i >= 0 {
		return prompt[:i] + "\n" + note + prompt[i:]
	}
	return prompt + "\n" + note
}
