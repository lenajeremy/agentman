package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// answeringSource records the answer the daemon hands it.
type answeringSource struct {
	streamingSource
	mu     sync.Mutex
	answer *protocol.QuestionAnswer
}

func (s *answeringSource) Answer(_ context.Context, _ string, answer protocol.QuestionAnswer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answer = &answer
	return nil
}

// "No, and do this instead" is an option chosen plus a note. Both have to
// reach the adapter, held to the rules any typed text is.
func TestAnOptionChosenWithANoteReachesTheAdapter(t *testing.T) {
	src := &answeringSource{}
	agent, _ := agentWithSource(t, src)
	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqAnswer, SessionID: "claude:s1", ClientID: "a1", QuestionID: "q",
		OptionKey: "3", AnswerText: "use the staging database instead",
	})
	if event.Status != protocol.StatusDelivered {
		t.Fatalf("answer = %+v", event)
	}
	src.mu.Lock()
	got := src.answer
	src.mu.Unlock()
	if got == nil || got.OptionKey != "3" || got.Text != "use the staging database instead" ||
		got.QuestionID != "q" || len(got.Options) != 0 {
		t.Fatalf("the adapter received %+v", got)
	}

	for name, req := range map[string]protocol.Request{
		"escape in the note": {AnswerText: "fine\x1b[2J"},
		"carriage return":    {AnswerText: "one\rtwo"},
		"note too long":      {AnswerText: strings.Repeat("x", maxMessageBytes+1)},
	} {
		req.Type, req.SessionID, req.QuestionID, req.OptionKey = protocol.ReqAnswer, "claude:s1", "q", "3"
		if err := validateRequest(req); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// The flag survives the daemon's bounding of a discovered question, or the
// phone would never offer the note.
func TestAnOptionsNoteFlagReachesThePhone(t *testing.T) {
	found := normalizeDiscoveredSessions([]protocol.Session{{
		ID: "kiro:k1", Kind: protocol.KindKiro, State: protocol.StateWaitingInput,
		Question: &protocol.Question{ID: "q", Prompt: "Run rm -rf build?", Options: []protocol.QuestionOption{
			{Key: "y", Label: "Yes"},
			{Key: "n", Label: "No", WithText: true},
		}},
	}})
	if len(found) != 1 || found[0].Question == nil || len(found[0].Question.Options) != 2 {
		t.Fatalf("normalized to %+v", found)
	}
	if options := found[0].Question.Options; options[0].WithText || !options[1].WithText {
		t.Fatalf("options = %+v", options)
	}
}
