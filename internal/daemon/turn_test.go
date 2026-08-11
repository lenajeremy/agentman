package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// scriptedSource reports whatever state the test sets, so a turn ending can be
// driven exactly rather than waited for. The live path cannot be tested against
// a real agent here: OpenCode's provider answers some prompts in well under the
// discovery interval, so a genuine turn can begin and end without discovery
// ever observing the busy state.
type scriptedSource struct {
	mu     sync.Mutex
	state  protocol.State
	reply  string
	failed string
}

func (s *scriptedSource) set(state protocol.State) {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
}

func (s *scriptedSource) Kind() protocol.Kind { return protocol.KindOpenCode }

func (s *scriptedSource) Discover(context.Context) ([]protocol.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return []protocol.Session{{
		ID:    "opencode:s1",
		Kind:  protocol.KindOpenCode,
		Name:  "checkout",
		State: s.state,
	}}, nil
}

func (s *scriptedSource) Page(_ context.Context, sessionID, _ string, _ int) (protocol.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	messages := []protocol.Message{
		{ID: "m1", SessionID: sessionID, Role: protocol.RoleUser, Text: "run the tests"},
		{ID: "m2", SessionID: sessionID, Role: protocol.RoleAssistant, Text: s.reply},
	}
	if s.failed != "" {
		// How a failed turn actually arrives: the assistant message is present
		// but empty, with the reason recorded after it.
		messages[1].Text = ""
		messages = append(messages, protocol.Message{
			ID: "m2:error", SessionID: sessionID, Role: protocol.RoleSystem, Text: s.failed,
		})
	}
	return protocol.NewPage(sessionID, messages, "", false), nil
}

func (s *scriptedSource) Follow(ctx context.Context, _ string, _ chan<- []protocol.Message) error {
	<-ctx.Done()
	return ctx.Err()
}

func (s *recordingSink) turnCompletes() []protocol.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []protocol.Event
	for _, event := range s.events {
		if event.Type == protocol.EvtTurnComplete {
			out = append(out, event)
		}
	}
	return out
}

// TestTurnCompleteWithoutHooks covers the agents that never deliver a hook.
//
// turn_complete used to be emitted in exactly one place — the hook handler —
// which meant "your agent is done" worked for Claude Code alone. Codex
// registers hooks and stays silent, and OpenCode has no hook system at all, so
// for two of the three agents the headline feature simply never fired.
func TestTurnCompleteWithoutHooks(t *testing.T) {
	registry := source.NewRegistry()
	scripted := &scriptedSource{state: protocol.StateBusy, reply: "All 42 tests passed."}
	registry.Add(scripted)

	sink := &recordingSink{}
	agent := New(registry, sink)
	ctx := context.Background()

	agent.refresh(ctx, true)  // first sight: busy
	agent.refresh(ctx, false) // still busy — nothing to announce

	if got := sink.turnCompletes(); len(got) != 0 {
		t.Fatalf("announced a completed turn while the agent was still working: %+v", got)
	}

	scripted.set(protocol.StateIdle)
	agent.refresh(ctx, false)

	events := sink.turnCompletes()
	if len(events) != 1 {
		t.Fatalf("got %d turn_complete events on busy→idle, want exactly 1", len(events))
	}
	if events[0].SessionID != "opencode:s1" {
		t.Errorf("SessionID = %q, want the session that finished", events[0].SessionID)
	}
	if events[0].SessionName != "checkout" {
		t.Errorf("SessionName = %q; a notification has to name the session", events[0].SessionName)
	}
	// A notification saying only "it finished" makes you open the app to learn
	// anything, which is the thing this is meant to save you.
	if !strings.Contains(events[0].Preview, "42 tests passed") {
		t.Errorf("Preview = %q, want the agent's closing words", events[0].Preview)
	}

	// Staying idle is not a new turn.
	agent.refresh(ctx, false)
	agent.refresh(ctx, false)
	if got := sink.turnCompletes(); len(got) != 1 {
		t.Errorf("got %d turn_complete events, want 1: an idle session re-announced itself", len(got))
	}
}

// TestHookWinsOverPolling checks the two paths do not both fire for one turn.
//
// Claude Code delivers hooks *and* is polled, so without suppression every
// finished Claude turn would buzz the phone twice for the same piece of work.
func TestHookWinsOverPolling(t *testing.T) {
	registry := source.NewRegistry()
	scripted := &scriptedSource{state: protocol.StateBusy, reply: "Done."}
	registry.Add(scripted)

	sink := &recordingSink{}
	agent := New(registry, sink)
	ctx := context.Background()

	agent.refresh(ctx, true)

	// The hook arrives first, as it does in practice: it fires the moment the
	// agent stops, while discovery only notices on its next sweep.
	agent.handleHook(hook.Event{
		Kind:      protocol.KindOpenCode,
		Name:      hook.NameStop,
		SessionID: "opencode:s1",
	})

	scripted.set(protocol.StateIdle)
	agent.refresh(ctx, false)

	if got := sink.turnCompletes(); len(got) != 1 {
		t.Errorf("got %d turn_complete events for one turn, want 1 — "+
			"the hook and the poller both announced it", len(got))
	}
}

// TestFailedTurnSaysSo covers the turn that ends because the provider fell over.
//
// Observed against a live `opencode serve`: the model endpoint returned 503, the
// session went busy→idle in under a second, and the notification said "done"
// with an empty preview — indistinguishable from success, for a turn that
// produced nothing at all.
func TestFailedTurnSaysSo(t *testing.T) {
	registry := source.NewRegistry()
	scripted := &scriptedSource{
		state:  protocol.StateBusy,
		reply:  "All 42 tests passed.",
		failed: "Provider request failed with HTTP 503: Endpoint is unavailable.",
	}
	registry.Add(scripted)

	sink := &recordingSink{}
	agent := New(registry, sink)
	ctx := context.Background()

	agent.refresh(ctx, true)
	scripted.set(protocol.StateIdle)
	agent.refresh(ctx, false)

	events := sink.turnCompletes()
	if len(events) != 1 {
		t.Fatalf("got %d turn_complete events, want 1", len(events))
	}
	if events[0].Preview == "" {
		t.Fatal("a turn that failed notified with an empty preview, which reads as success")
	}
	if !strings.Contains(events[0].Preview, "503") {
		t.Errorf("Preview = %q, want the reason the turn failed", events[0].Preview)
	}
}
