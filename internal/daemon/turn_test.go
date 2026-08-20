package daemon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

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
	mu       sync.Mutex
	state    protocol.State
	reply    string
	failed   string
	question *protocol.Question
}

type inspectingClaudeSource struct {
	streamingSource
	question      *protocol.Question
	inspectErrors int
}

func (s *inspectingClaudeSource) CurrentQuestion(
	context.Context,
	string,
) (*protocol.Question, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inspectErrors > 0 {
		s.inspectErrors--
		return nil, errors.New("tmux capture timed out")
	}
	return s.question, nil
}

func (s *scriptedSource) set(state protocol.State) {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
}

func (s *scriptedSource) setQuestion(state protocol.State, question *protocol.Question) {
	s.mu.Lock()
	s.state = state
	s.question = question
	s.mu.Unlock()
}

func (s *scriptedSource) Kind() protocol.Kind { return protocol.KindOpenCode }

func (s *scriptedSource) Discover(context.Context) ([]protocol.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return []protocol.Session{{
		ID:       "opencode:s1",
		Kind:     protocol.KindOpenCode,
		Name:     "checkout",
		State:    s.state,
		Question: s.question,
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

func TestTwoShortTurnsBothNotify(t *testing.T) {
	registry := source.NewRegistry()
	scripted := &scriptedSource{state: protocol.StateBusy, reply: "Done."}
	registry.Add(scripted)
	sink := &recordingSink{}
	agent := New(registry, sink)
	ctx := context.Background()

	agent.refresh(ctx, true)
	scripted.set(protocol.StateIdle)
	agent.refresh(ctx, false)
	scripted.set(protocol.StateBusy)
	agent.refresh(ctx, false)
	scripted.set(protocol.StateIdle)
	agent.refresh(ctx, false)

	if got := len(sink.turnCompletes()); got != 2 {
		t.Fatalf("two busy→idle turns produced %d notifications, want 2", got)
	}
}

func TestDuplicateClaudeStopHookNotifiesOnce(t *testing.T) {
	sink := &recordingSink{}
	registry := source.NewRegistry()
	registry.Add(&inspectingClaudeSource{})
	agent := New(registry, sink)
	agent.turnDelay = 0
	agent.sessions["claude:s1"] = protocol.Session{
		ID: "claude:s1", Kind: protocol.KindClaude, Name: "checkout", State: protocol.StateIdle,
	}
	event := hook.Event{
		Kind: protocol.KindClaude, Name: hook.NameStop, SessionID: "claude:s1",
		Payload: hook.Payload{LastAssistantMessage: "Done."},
	}
	agent.handleHook(event)
	agent.handleHook(event)

	if got := len(sink.turnCompletes()); got != 1 {
		t.Fatalf("duplicate Claude Stop hooks produced %d notifications, want 1", got)
	}
}

func TestClaudeStopChecksLiveQuestionWithoutWaitingForFullDiscovery(t *testing.T) {
	registry := source.NewRegistry()
	registry.Add(&inspectingClaudeSource{question: &protocol.Question{
		ID: "terminal-current", Prompt: "Choose a path",
		Options: []protocol.QuestionOption{{Key: "1", Label: "Local"}},
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

	if got := len(sink.turnCompletes()); got != 0 {
		t.Fatalf("question-producing Stop hook emitted %d completion notifications", got)
	}
	agent.mu.Lock()
	current := agent.sessions["claude:s1"]
	agent.mu.Unlock()
	if current.State != protocol.StateWaitingInput || current.Question == nil ||
		current.Question.ID != "terminal-current" {
		t.Fatalf("live question was not surfaced by targeted inspection: %+v", current)
	}
}

func TestClaudeStopRetriesInspectionErrorInsteadOfFalseCompletion(t *testing.T) {
	registry := source.NewRegistry()
	registry.Add(&inspectingClaudeSource{
		inspectErrors: 1,
		question: &protocol.Question{
			ID: "terminal-current", Prompt: "Choose a path",
			Options: []protocol.QuestionOption{{Key: "1", Label: "Local"}},
		},
	})
	sink := &recordingSink{}
	agent := New(registry, sink)
	agent.turnDelay = 0
	agent.questionRetryDelay = 5 * time.Millisecond
	agent.sessions["claude:s1"] = protocol.Session{
		ID: "claude:s1", Kind: protocol.KindClaude, Name: "checkout", State: protocol.StateIdle,
	}

	agent.handleHook(hook.Event{
		Kind: protocol.KindClaude, Name: hook.NameStop, SessionID: "claude:s1",
	})
	t.Cleanup(agent.stopPendingTurns)

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		agent.mu.Lock()
		current := agent.sessions["claude:s1"]
		agent.mu.Unlock()
		if current.Question != nil {
			if got := len(sink.turnCompletes()); got != 0 {
				t.Fatalf("capture error produced %d false completion notifications", got)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("targeted question inspection did not recover after transient capture error")
}

func TestEachCodexNotifyRepresentsANewTurn(t *testing.T) {
	sink := &recordingSink{}
	agent := New(source.NewRegistry(), sink)
	agent.turnDelay = 0
	agent.sessions["codex:s1"] = protocol.Session{
		ID: "codex:s1", Kind: protocol.KindCodex, NativeID: "s1",
		Name: "checkout", State: protocol.StateIdle,
	}
	event := hook.Event{
		Kind: protocol.KindCodex, Name: hook.NameStop, SessionID: "codex:s1",
		Payload: hook.Payload{LastAssistantMessage: "Done."},
	}
	agent.handleHook(event)
	event.Payload.LastAssistantMessage = "Done again."
	agent.handleHook(event)

	if got := len(sink.turnCompletes()); got != 2 {
		t.Fatalf("two Codex notify callbacks produced %d notifications, want 2", got)
	}
}

func TestStalePollCannotUndoBusyHook(t *testing.T) {
	registry := source.NewRegistry()
	scripted := &scriptedSource{state: protocol.StateIdle, reply: "Done."}
	registry.Add(scripted)
	sink := &recordingSink{}
	agent := New(registry, sink)
	ctx := context.Background()
	agent.refresh(ctx, true)

	agent.handleHook(hook.Event{
		Kind: protocol.KindOpenCode, Name: hook.NameUserPromptSubmit,
		SessionID: "opencode:s1", ReceivedAt: time.Now().UnixMilli(),
	})
	// The source is deliberately still idle: this is the one-sweep lag that
	// used to overwrite the hook and emit a false completion immediately.
	agent.refresh(ctx, false)

	agent.mu.Lock()
	state := agent.sessions["opencode:s1"].State
	agent.mu.Unlock()
	if state != protocol.StateBusy {
		t.Fatalf("stale poll overwrote busy hook with %q", state)
	}
	if got := len(sink.turnCompletes()); got != 0 {
		t.Fatalf("stale poll produced %d false completion notifications", got)
	}
}

func TestWaitingHookPersistsAcrossIdlePolls(t *testing.T) {
	registry := source.NewRegistry()
	scripted := &scriptedSource{state: protocol.StateIdle}
	registry.Add(scripted)
	agent := New(registry, &recordingSink{})
	ctx := context.Background()
	agent.refresh(ctx, true)

	agent.handleHook(hook.Event{
		Kind: protocol.KindOpenCode, Name: hook.NameNotification,
		SessionID: "opencode:s1", ReceivedAt: time.Now().Add(-time.Hour).UnixMilli(),
	})
	agent.refresh(ctx, false)

	agent.mu.Lock()
	state := agent.sessions["opencode:s1"].State
	agent.mu.Unlock()
	if state != protocol.StateWaitingInput {
		t.Fatalf("idle poll erased an unresolved waiting hook: %q", state)
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
	agent.turnDelay = 0
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

func TestCodexHookUsesStableTmuxSessionID(t *testing.T) {
	sink := &recordingSink{}
	agent := New(source.NewRegistry(), sink)
	agent.turnDelay = 0
	agent.sessions["codex:tmux-pane-1"] = protocol.Session{
		ID: "codex:tmux-pane-1", Kind: protocol.KindCodex,
		NativeID: "thread-123", Name: "agentman", State: protocol.StateBusy,
	}

	agent.handleHook(hook.Event{
		Kind: protocol.KindCodex, Name: hook.NameStop,
		SessionID: "codex:thread-123",
		Payload:   hook.Payload{LastAssistantMessage: "Done."},
	})

	events := sink.turnCompletes()
	if len(events) != 1 {
		t.Fatalf("turn completes = %+v, want one", events)
	}
	if events[0].SessionID != "codex:tmux-pane-1" || events[0].SessionName != "agentman" {
		t.Fatalf("hook was not canonicalized: %+v", events[0])
	}
	if _, invented := agent.sessions["codex:thread-123"]; invented {
		t.Fatal("hook created state under the native thread id")
	}
	if got := agent.sessions["codex:tmux-pane-1"].State; got != protocol.StateIdle {
		t.Fatalf("canonical session state = %q, want idle", got)
	}
}

// Claude uses the same Stop hook when a turn finishes and when AskUserQuestion
// blocks. Discovery sees the tmux question on the next sweep; the delayed hook
// announcement must yield to that more specific state.
func TestQuestionSuppressesHookCompletion(t *testing.T) {
	registry := source.NewRegistry()
	scripted := &scriptedSource{state: protocol.StateBusy, reply: "Which database?"}
	registry.Add(scripted)

	sink := &recordingSink{}
	agent := New(registry, sink)
	agent.turnDelay = 30 * time.Millisecond
	ctx := context.Background()
	agent.refresh(ctx, true)

	agent.handleHook(hook.Event{
		Kind: protocol.KindClaude, Name: hook.NameStop, SessionID: "opencode:s1",
	})
	scripted.setQuestion(protocol.StateWaitingInput, &protocol.Question{
		ID: "terminal-database", Prompt: "Which database?",
		Options: []protocol.QuestionOption{
			{Key: "1", Label: "Postgres"},
			{Key: "2", Label: "SQLite"},
		},
	})
	agent.refresh(ctx, false)
	time.Sleep(60 * time.Millisecond)

	if got := sink.turnCompletes(); len(got) != 0 {
		t.Fatalf("question produced %d false turn-complete alerts: %+v", len(got), got)
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
