package manager

import (
	"context"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func openCards(h *harness) int {
	return len(h.m.conversation.openCards())
}

func pushing() protocol.Session {
	return protocol.Session{ID: "codex:b", Kind: "codex", Name: "api", Cwd: "/w/api", State: protocol.StateWaitingInput,
		Inject: protocol.InjectTmux, Question: permission("q9", "git push origin main")}
}

// The agent the user is looking at gets no card and no push for its
// question: it is in front of them. Looked away from while it still asks,
// its card goes up then.
func TestNoCardForTheAgentOnScreen(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, &scriptedModel{}, true)
	session := pushing()
	h.agents.setSessions([]protocol.Session{session})

	h.m.setViewing(ctx, "mac", []string{"codex:b"})
	h.m.takeQuestion(ctx, session)
	if n := openCards(h); n != 0 {
		t.Fatalf("%d cards for the agent on screen", n)
	}
	if told := h.told(); len(told) != 0 {
		t.Fatalf("pushed %v for the agent on screen", told)
	}

	// Another app's word doesn't take the first one's away.
	h.m.setViewing(ctx, "phone", nil)
	if n := openCards(h); n != 0 {
		t.Fatalf("%d cards while the Mac still shows the agent", n)
	}

	h.m.setViewing(ctx, "mac", []string{"claude:a"})
	if n := openCards(h); n != 1 {
		t.Fatalf("%d cards once the user looked away, want 1", n)
	}
	if told := h.told(); len(told) != 1 || told[0] != "card" {
		t.Fatalf("pushed %v once the user looked away", told)
	}
}

// A question answered while the user looked never becomes a card.
func TestAQuestionAnsweredWhileLookingNeverComesUp(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, &scriptedModel{}, true)
	session := pushing()
	h.agents.setSessions([]protocol.Session{session})
	h.m.setViewing(ctx, "mac", []string{"codex:b"})
	h.m.takeQuestion(ctx, session)

	session.Question, session.State = nil, protocol.StateBusy
	h.agents.setSessions([]protocol.Session{session})
	h.m.setViewing(ctx, "mac", nil)
	if n := openCards(h); n != 0 {
		t.Fatalf("%d cards for a question answered while the user looked", n)
	}
}

// An app's word lasts two minutes: one that stops saying what it shows
// (quit, crashed, asleep) holds nothing back for longer.
func TestAnAppThatStopsSayingWhatItShowsHoldsNothingBack(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, &scriptedModel{}, true)
	clock := time.Now()
	h.m.now = func() time.Time { return clock }
	session := pushing()
	h.agents.setSessions([]protocol.Session{session})
	h.m.setViewing(ctx, "mac", []string{"codex:b"})
	h.m.takeQuestion(ctx, session)
	if n := openCards(h); n != 0 {
		t.Fatalf("%d cards while the app said it showed the agent", n)
	}
	clock = clock.Add(viewingFor + time.Second)
	h.m.raiseHeldCards(ctx)
	if n := openCards(h); n != 1 {
		t.Fatalf("%d cards after the app went quiet, want 1", n)
	}
}

// News about the agent on screen is kept in the conversation but not
// pushed; news about another agent still is.
func TestNoPushAboutTheAgentOnScreen(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, &scriptedModel{}, true)
	h.m.setViewing(ctx, "mac", []string{"claude:a"})
	h.m.addEntry(protocol.ManagerEntry{Role: "brief", Text: "site finished the header.", SessionID: "claude:a"})
	if told := h.told(); len(told) != 0 {
		t.Fatalf("pushed %v about the agent on screen", told)
	}
	h.m.addEntry(protocol.ManagerEntry{Role: "brief", Text: "api's tests pass.", SessionID: "codex:b"})
	if told := h.told(); len(told) != 1 {
		t.Fatalf("pushed %v, want the other agent's news", told)
	}
}
