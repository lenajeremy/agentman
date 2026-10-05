package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

func encodedEvent(t *testing.T, event protocol.Event) string {
	t.Helper()
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The app reads "sessions" as an array and drops a frame without one. An
// empty list therefore has to travel as [], or the phone keeps showing
// sessions that have all ended.
func TestAnEmptySessionListIsSentAsAnEmptyArray(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	agent := New(source.NewRegistry(), &recordingSink{})
	ctx := context.Background()

	list := agent.HandleFrom(ctx, "phone", protocol.Request{Type: protocol.ReqListSessions})
	if got := encodedEvent(t, list); !strings.Contains(got, `"sessions":[]`) {
		t.Errorf("an empty board was sent as %s", got)
	}

	folder := agent.HandleFrom(ctx, "phone", protocol.Request{
		Type: protocol.ReqDirectorySessions, Path: t.TempDir(),
	})
	if got := encodedEvent(t, folder); !strings.Contains(got, `"sessions":[]`) {
		t.Errorf("an empty folder was sent as %s", got)
	}
}

// More than a handful of sessions ending in one sweep is reported as one
// snapshot instead of a session_gone each, so when none remain that snapshot
// is the only thing telling the phone they ended.
func TestEveryoneEndingAtOnceSendsAnEmptySnapshot(t *testing.T) {
	bulk := &bulkDiscoverySource{}
	for i := range maxIncrementalSessionEvents + 1 {
		bulk.sessions = append(bulk.sessions, protocol.Session{
			ID: "claude:s" + string(rune('a'+i)), Kind: protocol.KindClaude, State: protocol.StateIdle,
		})
	}
	registry := source.NewRegistry()
	registry.Add(bulk)
	sink := &recordingSink{}
	agent := New(registry, sink)
	ctx := context.Background()
	agent.refresh(ctx, true)

	bulk.sessions = nil
	agent.refresh(ctx, false)

	sink.mu.Lock()
	defer sink.mu.Unlock()
	last := sink.events[len(sink.events)-1]
	if last.Type != protocol.EvtSessions {
		t.Fatalf("expected a snapshot, got %s", last.Type)
	}
	if got := encodedEvent(t, last); !strings.Contains(got, `"sessions":[]`) {
		t.Errorf("the snapshot that ends every session was sent as %s", got)
	}
}

// A question whose options cannot be shown safely keeps its prompt and says to
// answer in the terminal. Its options must still be a list: the app rejects a
// question without one, and with it the whole session list it arrived in.
func TestWithdrawnOptionsTravelAsAnEmptyList(t *testing.T) {
	options := make([]protocol.QuestionOption, maxWireOptions+1)
	for i := range options {
		options[i] = protocol.QuestionOption{Key: string(rune('a' + i%26)), Label: "choice"}
	}
	normalized := normalizeDiscoveredSessions([]protocol.Session{{
		ID: "opencode:s1", Kind: protocol.KindOpenCode, State: protocol.StateWaitingInput,
		Question: &protocol.Question{ID: "q1", Prompt: "Pick one", Options: options},
	}})
	got := encodedEvent(t, protocol.Event{Type: protocol.EvtSessions, Sessions: normalized})
	if !strings.Contains(got, `"options":[]`) || strings.Contains(got, `"options":null`) {
		t.Errorf("withdrawn options were sent as %s", got)
	}
}

// The question a Stop hook finds by inspecting the pane goes to the phone
// directly, not through a discovery sweep, so it has to be held to the same
// bounds discovery applies.
func TestAQuestionFoundAfterAStopHookIsBoundedLikeDiscovery(t *testing.T) {
	options := make([]protocol.QuestionOption, maxWireOptions+1)
	for i := range options {
		options[i] = protocol.QuestionOption{Key: string(rune('a' + i%26)), Label: "choice"}
	}
	registry := source.NewRegistry()
	registry.Add(&inspectingClaudeSource{question: &protocol.Question{
		ID: "terminal-current", Prompt: "Choose", Options: options,
	}})
	sink := &recordingSink{}
	agent := New(registry, sink)
	agent.turnDelay = 0
	agent.sessions["claude:s1"] = protocol.Session{
		ID: "claude:s1", Kind: protocol.KindClaude, Name: "checkout", State: protocol.StateIdle,
	}

	agent.handleHook(hook.Event{
		Kind: protocol.KindClaude, Name: hook.NameStop, SessionID: "claude:s1",
		Payload: hook.Payload{LastAssistantMessage: "Done."},
	})

	sink.mu.Lock()
	defer sink.mu.Unlock()
	var update *protocol.Session
	for _, event := range sink.events {
		if event.Type == protocol.EvtSessionUpdate && event.Session != nil && event.Session.Question != nil {
			update = event.Session
		}
	}
	if update == nil {
		t.Fatal("the question was not reported")
	}
	if got := encodedEvent(t, protocol.Event{Type: protocol.EvtSessionUpdate, Session: update}); !strings.Contains(got, `"options":[]`) {
		t.Errorf("an unbounded hook question reached the phone: %s", got)
	}
}
