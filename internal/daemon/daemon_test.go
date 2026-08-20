package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// recordingSink captures what the daemon would send to a connected app.
type recordingSink struct {
	mu     sync.Mutex
	events []protocol.Event
}

func (s *recordingSink) Send(event protocol.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *recordingSink) messageCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	for _, event := range s.events {
		if event.Type == protocol.EvtMessages {
			n += len(event.Messages)
		}
	}
	return n
}

// streamingSource is a Source that emits a message every tick, standing in for
// an agent that is actively working.
type streamingSource struct {
	mu      sync.Mutex
	follows int
}

type bulkDiscoverySource struct {
	streamingSource
	sessions []protocol.Session
}

func (s *bulkDiscoverySource) Discover(context.Context) ([]protocol.Session, error) {
	return append([]protocol.Session(nil), s.sessions...), nil
}

func (s *streamingSource) Kind() protocol.Kind { return protocol.KindClaude }

func (s *streamingSource) Discover(context.Context) ([]protocol.Session, error) {
	return []protocol.Session{{ID: "claude:s1", Kind: protocol.KindClaude, Name: "s1"}}, nil
}

func (s *streamingSource) Page(context.Context, string, string, int) (protocol.Page, error) {
	return protocol.NewPage("claude:s1", nil, "", false), nil
}

func (s *streamingSource) activeFollows() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.follows
}

func (s *streamingSource) Follow(ctx context.Context, sessionID string, out chan<- []protocol.Message) error {
	s.mu.Lock()
	s.follows++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.follows--
		s.mu.Unlock()
	}()

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			select {
			case out <- []protocol.Message{{ID: "m", SessionID: sessionID, Text: "tick"}}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// TestResubscribeKeepsStreaming reproduces the bug that made the app show a
// live session with no messages while `am watch` on the same session worked.
//
// An app that remounts a screen — React's development double-invoke, or the
// user navigating back and then in again — sends subscribe, unsubscribe,
// subscribe. The first tail's cleanup used to cancel by session id, so it
// tore down the *second* tail: the app stayed subscribed to a stream that had
// already been shut down and received nothing, silently and permanently.
func TestResubscribeKeepsStreaming(t *testing.T) {
	registry := source.NewRegistry()
	streaming := &streamingSource{}
	registry.Add(streaming)

	sink := &recordingSink{}
	agent := New(registry, sink)
	ctx := context.Background()
	agent.refresh(ctx, true)

	agent.Handle(ctx, protocol.Request{Type: protocol.ReqSubscribe, SessionID: "claude:s1"})
	agent.Handle(ctx, protocol.Request{Type: protocol.ReqUnsubscribe, SessionID: "claude:s1"})
	agent.Handle(ctx, protocol.Request{Type: protocol.ReqSubscribe, SessionID: "claude:s1"})

	// Long enough for the first tail's deferred cleanup to have run.
	time.Sleep(300 * time.Millisecond)

	if got := sink.messageCount(); got == 0 {
		t.Fatal("no messages after resubscribing: the second tail was cancelled " +
			"by the first one's cleanup")
	}

	agent.Handle(ctx, protocol.Request{Type: protocol.ReqUnsubscribe, SessionID: "claude:s1"})
	time.Sleep(100 * time.Millisecond)
	if got := streaming.activeFollows(); got != 0 {
		t.Errorf("%d tails still running after unsubscribe; they must not leak", got)
	}
}

func TestUnsubscribeStopsStreaming(t *testing.T) {
	registry := source.NewRegistry()
	registry.Add(&streamingSource{})

	sink := &recordingSink{}
	agent := New(registry, sink)
	ctx := context.Background()
	agent.refresh(ctx, true)

	agent.Handle(ctx, protocol.Request{Type: protocol.ReqSubscribe, SessionID: "claude:s1"})
	time.Sleep(120 * time.Millisecond)
	agent.Handle(ctx, protocol.Request{Type: protocol.ReqUnsubscribe, SessionID: "claude:s1"})
	time.Sleep(60 * time.Millisecond)

	// Idle sessions must cost nothing, which is what makes the zero-storage
	// design affordable — a tail that outlives its subscription breaks that.
	settled := sink.messageCount()
	time.Sleep(150 * time.Millisecond)
	if after := sink.messageCount(); after != settled {
		t.Errorf("messages kept arriving after unsubscribe: %d then %d", settled, after)
	}
}

func TestSubscribeIsIdempotent(t *testing.T) {
	registry := source.NewRegistry()
	streaming := &streamingSource{}
	registry.Add(streaming)

	agent := New(registry, &recordingSink{})
	ctx := context.Background()
	agent.refresh(ctx, true)

	for range 4 {
		agent.Handle(ctx, protocol.Request{Type: protocol.ReqSubscribe, SessionID: "claude:s1"})
	}
	time.Sleep(80 * time.Millisecond)

	if got := streaming.activeFollows(); got != 1 {
		t.Errorf("%d tails running for one session, want 1", got)
	}
}

func TestUnsubscribeOnlyStopsTheCallingDevice(t *testing.T) {
	registry := source.NewRegistry()
	streaming := &streamingSource{}
	registry.Add(streaming)

	agent := New(registry, &recordingSink{})
	ctx := context.Background()
	agent.refresh(ctx, true)
	agent.HandleFrom(ctx, "phone", protocol.Request{Type: protocol.ReqSubscribe, SessionID: "claude:s1"})
	agent.HandleFrom(ctx, "tablet", protocol.Request{Type: protocol.ReqSubscribe, SessionID: "claude:s1"})
	time.Sleep(80 * time.Millisecond)

	agent.HandleFrom(ctx, "phone", protocol.Request{Type: protocol.ReqUnsubscribe, SessionID: "claude:s1"})
	time.Sleep(60 * time.Millisecond)
	if got := streaming.activeFollows(); got != 1 {
		t.Fatalf("one device stopped another device's tail; active = %d", got)
	}

	agent.DisconnectSubscriber("tablet")
	time.Sleep(60 * time.Millisecond)
	if got := streaming.activeFollows(); got != 0 {
		t.Fatalf("disconnected device left %d tails running", got)
	}
}

type interruptingSource struct {
	streamingSource
	interrupted bool
}

func (s *interruptingSource) Interrupt(context.Context, string) error {
	s.mu.Lock()
	s.interrupted = true
	s.mu.Unlock()
	return nil
}

func TestInterruptRequestReachesSource(t *testing.T) {
	registry := source.NewRegistry()
	controlled := &interruptingSource{}
	registry.Add(controlled)
	agent := New(registry, &recordingSink{})

	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqInterrupt, SessionID: "claude:s1", ClientID: "stop-1",
	})
	if event.Type != protocol.EvtSendResult || event.Status != protocol.StatusDelivered {
		t.Fatalf("interrupt result = %+v", event)
	}
	controlled.mu.Lock()
	interrupted := controlled.interrupted
	controlled.mu.Unlock()
	if !interrupted {
		t.Fatal("interrupt request never reached the source")
	}
}

func TestOversizedMessageIsRejectedBeforeInjection(t *testing.T) {
	agent := New(source.NewRegistry(), &recordingSink{})
	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:s1",
		ClientID: "send-1", Text: strings.Repeat("x", maxMessageBytes+1),
	})
	if event.Type != protocol.EvtSendResult || event.Status != protocol.StatusFailed {
		t.Fatalf("oversized request result = %+v", event)
	}
}

type concurrentActionSource struct {
	streamingSource
	active atomic.Int32
	peak   atomic.Int32
	calls  atomic.Int32
}

func (s *concurrentActionSource) Inject(
	context.Context,
	string,
	string,
) (protocol.InjectMode, error) {
	s.calls.Add(1)
	current := s.active.Add(1)
	for {
		previous := s.peak.Load()
		if current <= previous || s.peak.CompareAndSwap(previous, current) {
			break
		}
	}
	time.Sleep(15 * time.Millisecond)
	s.active.Add(-1)
	return protocol.InjectTmux, nil
}

func TestMessageCannotTypeIntoPendingQuestion(t *testing.T) {
	registry := source.NewRegistry()
	controlled := &concurrentActionSource{}
	registry.Add(controlled)
	agent := New(registry, &recordingSink{})
	agent.sessions["claude:s1"] = protocol.Session{
		ID: "claude:s1", Kind: protocol.KindClaude,
		Question: &protocol.Question{
			Prompt: "Approve?", Options: []protocol.QuestionOption{{Key: "1", Label: "Yes"}},
		},
	}

	result := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:s1",
		ClientID: "send-1", Text: "this must not reach the menu",
	})
	if result.Status != protocol.StatusFailed || !strings.Contains(result.Error, "pending question") {
		t.Fatalf("send result = %+v", result)
	}
	if got := controlled.calls.Load(); got != 0 {
		t.Fatalf("injector called %d times while a question was pending", got)
	}
}

func TestSessionMutationsAreSerialized(t *testing.T) {
	registry := source.NewRegistry()
	controlled := &concurrentActionSource{}
	registry.Add(controlled)
	agent := New(registry, &recordingSink{})

	const actions = 12
	start := make(chan struct{})
	results := make(chan protocol.Event, actions)
	var wait sync.WaitGroup
	for index := range actions {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- agent.Handle(context.Background(), protocol.Request{
				Type: protocol.ReqSendMessage, SessionID: "claude:s1",
				ClientID: fmt.Sprintf("send-%d", index), Text: "continue",
			})
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for result := range results {
		if result.Status != protocol.StatusDelivered {
			t.Fatalf("mutation failed: %+v", result)
		}
	}
	if got := controlled.peak.Load(); got != 1 {
		t.Fatalf("%d mutations reached one session concurrently, want 1", got)
	}
}

func TestRequestValidationRejectsTerminalControlCharacters(t *testing.T) {
	tests := []protocol.Request{
		{Type: protocol.ReqSendMessage, SessionID: "claude:s1", Text: "hello\x1b[A"},
		{Type: protocol.ReqSendMessage, SessionID: "claude:s1", Text: "hello\x00world"},
		{Type: protocol.ReqSendMessage, SessionID: "claude:s1", Text: "hello\rworld"},
		{Type: protocol.ReqSendMessage, SessionID: "claude:s1", Text: "hello\u0085world"},
		{Type: protocol.ReqAnswer, SessionID: "claude:s1", OptionKey: "1\x1b[201~"},
		{Type: protocol.ReqAnswer, SessionID: "claude:s1", OptionKeys: []string{"1", "2\x00"}},
		{Type: protocol.ReqAnswer, SessionID: "claude:s1", AnswerText: "yes\x1b[201~"},
	}
	for _, request := range tests {
		if err := validateRequest(request); err == nil {
			t.Errorf("accepted terminal control characters in %+v", request)
		}
	}
	if err := validateRequest(protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:s1", Text: "line one\n\tline two",
	}); err != nil {
		t.Fatalf("rejected ordinary multiline text: %v", err)
	}
}

func TestQuestionAnswerRequiresRevisionToken(t *testing.T) {
	if err := validateRequest(protocol.Request{
		Type: protocol.ReqAnswer, SessionID: "claude:s1", OptionKey: "1",
	}); err == nil {
		t.Fatal("answer without question revision was accepted")
	}
	if err := validateRequest(protocol.Request{
		Type: protocol.ReqAnswer, SessionID: "claude:s1",
		QuestionID: "terminal-current", OptionKey: "1",
	}); err != nil {
		t.Fatalf("answer with question revision was rejected: %v", err)
	}
}

func TestHistoryEventFitsRelayFrameWithoutDroppingMessages(t *testing.T) {
	messages := make([]protocol.Message, maxPageMessages)
	for i := range messages {
		messages[i] = protocol.Message{
			ID: "message", SessionID: "claude:s1", Role: protocol.RoleAssistant,
			// Control characters exercise JSON's worst-case escaping expansion.
			Text: strings.Repeat("\x00", maxWireMessageBytes),
		}
	}
	page := protocol.NewPage("claude:s1", messages, "next", true)
	event := fitMessageEvent(protocol.Event{Type: protocol.EvtPage, Page: &page})
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > maxEventBytes {
		t.Fatalf("event is %d bytes, limit is %d", len(encoded), maxEventBytes)
	}
	if len(event.Page.Messages) != len(messages) {
		t.Fatalf("fit dropped messages: got %d, want %d", len(event.Page.Messages), len(messages))
	}
	if event.Page.Messages[0].Text == messages[0].Text {
		t.Fatal("oversized text was not truncated")
	}
	if messages[0].Text != strings.Repeat("\x00", maxWireMessageBytes) {
		t.Fatal("fit mutated source messages")
	}
}

func TestWireTruncationPreservesUTF8(t *testing.T) {
	got := truncateWireText(strings.Repeat("🙂", 100), 101)
	if !strings.HasSuffix(got, wireTruncation) {
		t.Fatalf("missing truncation marker: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncation produced invalid UTF-8: %q", got)
	}
}

func TestDiscoveredSessionsAreBoundedWithoutMutatingSource(t *testing.T) {
	originalPrompt := strings.Repeat("question\x00", maxWirePromptBytes)
	originalPreview := strings.Repeat("preview\x00", maxWirePreviewBytes)
	input := []protocol.Session{{
		ID: "claude:s1", Kind: protocol.KindClaude, NativeID: "s1",
		Name:  strings.Repeat("name", maxWireNameBytes),
		Cwd:   strings.Repeat("/directory", maxWirePathBytes),
		State: protocol.StateWaitingInput,
		Question: &protocol.Question{
			ID: "terminal-revision", Prompt: originalPrompt,
			Options: []protocol.QuestionOption{
				{Key: "1", Label: strings.Repeat("label", maxWireOptionBytes), Preview: originalPreview},
			},
		},
	}}

	got := normalizeDiscoveredSessions(input)
	if len(got) != 1 || got[0].Question == nil {
		t.Fatalf("normalized sessions = %+v", got)
	}
	if len(got[0].Question.Options) != 1 || got[0].Question.Options[0].Key != "1" {
		t.Fatalf("invalid option key survived normalization: %+v", got[0].Question.Options)
	}
	if len(got[0].Question.Prompt) > maxWirePromptBytes ||
		len(got[0].Question.Options[0].Preview) > maxWirePreviewBytes {
		t.Fatalf("wire text exceeded its bound: %+v", got[0].Question)
	}
	if input[0].Question.Prompt != originalPrompt ||
		input[0].Question.Options[0].Preview != originalPreview ||
		len(input[0].Question.Options) != 1 {
		t.Fatal("normalization mutated the adapter's source snapshot")
	}
}

func TestSessionListFitsRelayFrameAndKeepsActionableSessions(t *testing.T) {
	sessions := make([]protocol.Session, maxWireSessions)
	for index := range sessions {
		sessions[index] = protocol.Session{
			ID: fmt.Sprintf("claude:%d", index), Kind: protocol.KindClaude,
			NativeID: fmt.Sprintf("%d", index), State: protocol.StateIdle,
			Name: strings.Repeat("\x00", maxWireNameBytes),
		}
	}
	sessions[0].State = protocol.StateWaitingInput
	sessions[0].Question = &protocol.Question{
		ID: "terminal-revision", Prompt: "Approve?",
		Options: []protocol.QuestionOption{{Key: "1", Label: "Yes"}},
	}

	normalized := normalizeDiscoveredSessions(sessions)
	fitted := fitSessionList(normalized)
	encoded, err := json.Marshal(protocol.Event{Type: protocol.EvtSessions, Sessions: fitted})
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > maxEventBytes {
		t.Fatalf("sessions event is %d bytes, limit is %d", len(encoded), maxEventBytes)
	}
	if len(fitted) == 0 || fitted[0].ID != "claude:0" || fitted[0].Question == nil {
		t.Fatalf("actionable first session was not retained: %+v", fitted)
	}
}

func TestOverlongQuestionRevisionIsNotTruncatedIntoAnUnanswerableToken(t *testing.T) {
	got := normalizeDiscoveredSessions([]protocol.Session{{
		ID: "claude:s1", Kind: protocol.KindClaude, NativeID: "s1",
		State: protocol.StateWaitingInput,
		Question: &protocol.Question{
			ID: strings.Repeat("r", maxClientIDBytes+1), Prompt: "Approve?",
			Options: []protocol.QuestionOption{{Key: "1", Label: "Yes"}},
		},
	}})
	if len(got) != 1 || got[0].Question != nil || got[0].State != protocol.StateIdle {
		t.Fatalf("overlong concurrency token was exposed: %+v", got)
	}
}

func TestWorstCaseQuestionUpdateFitsRelayFrame(t *testing.T) {
	options := make([]protocol.QuestionOption, maxWireOptions)
	for index := range options {
		options[index] = protocol.QuestionOption{
			Key:         fmt.Sprintf("option-%d", index),
			Label:       strings.Repeat("\x00", maxWireOptionBytes*2),
			Description: strings.Repeat("\x00", maxWireOptionBytes*2),
			Preview:     strings.Repeat("\x00", maxWirePreviewBytes*2),
		}
	}
	got := normalizeDiscoveredSessions([]protocol.Session{{
		ID: "claude:s1", Kind: protocol.KindClaude, NativeID: "s1",
		Name:  strings.Repeat("\x00", maxWireNameBytes*2),
		Cwd:   strings.Repeat("\x00", maxWirePathBytes*2),
		State: protocol.StateWaitingInput,
		Question: &protocol.Question{
			ID:      "terminal-revision",
			Title:   strings.Repeat("\x00", maxWireNameBytes*2),
			Prompt:  strings.Repeat("\x00", maxWirePromptBytes*2),
			Detail:  strings.Repeat("\x00", maxWireDetailBytes*2),
			Options: options,
		},
	}})
	if len(got) != 1 {
		t.Fatalf("normalization dropped worst-case bounded session")
	}
	encoded, err := json.Marshal(protocol.Event{Type: protocol.EvtSessionUpdate, Session: &got[0]})
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > maxEventBytes {
		t.Fatalf("worst-case session update is %d bytes, limit is %d", len(encoded), maxEventBytes)
	}
}

func TestQuestionOptionsAreCompleteOrExplicitlyUnsupported(t *testing.T) {
	seventeen := make([]protocol.QuestionOption, 17)
	for index := range seventeen {
		seventeen[index] = protocol.QuestionOption{
			Key: fmt.Sprint(index + 1), Label: fmt.Sprintf("Choice %d", index+1),
			Checked: index == 16,
		}
	}
	preserved := normalizeDiscoveredSessions([]protocol.Session{{
		ID: "claude:s1", Kind: protocol.KindClaude, NativeID: "s1",
		State: protocol.StateWaitingInput,
		Question: &protocol.Question{
			ID: "terminal-seventeen", Prompt: "Choose", Multiple: true, Options: seventeen,
		},
	}})
	if got := len(preserved[0].Question.Options); got != len(seventeen) ||
		!preserved[0].Question.Options[16].Checked {
		t.Fatalf("answerable options were truncated: %+v", preserved[0].Question)
	}

	tooMany := append(append([]protocol.QuestionOption(nil), seventeen...),
		make([]protocol.QuestionOption, maxWireOptions-len(seventeen)+1)...)
	for index := len(seventeen); index < len(tooMany); index++ {
		tooMany[index] = protocol.QuestionOption{Key: fmt.Sprint(index + 1), Label: "More"}
	}
	unsupported := normalizeDiscoveredSessions([]protocol.Session{{
		ID: "claude:s2", Kind: protocol.KindClaude, NativeID: "s2",
		State: protocol.StateWaitingInput,
		Question: &protocol.Question{
			ID: "terminal-too-many", Prompt: "Choose", Multiple: true, Options: tooMany,
		},
	}})
	question := unsupported[0].Question
	if question == nil || len(question.Options) != 0 ||
		!strings.Contains(question.Detail, "too many options") ||
		question.Custom || question.Multiple ||
		unsupported[0].State != protocol.StateWaitingInput {
		t.Fatalf("oversized decision was silently made partial: %+v", unsupported[0])
	}

	invalid := normalizeDiscoveredSessions([]protocol.Session{{
		ID: "claude:s3", Kind: protocol.KindClaude, NativeID: "s3",
		State: protocol.StateWaitingInput,
		Question: &protocol.Question{
			ID: "terminal-invalid", Prompt: "Choose", Options: []protocol.QuestionOption{
				{Key: "1", Label: "Visible"},
				{Key: "bad\x00key", Label: "Unsafe"},
			},
		},
	}})
	question = invalid[0].Question
	if question == nil || len(question.Options) != 0 ||
		!strings.Contains(question.Detail, "cannot be represented safely") {
		t.Fatalf("invalid option left an answerable partial question: %+v", invalid[0])
	}
}

func TestRefreshCoalescesLargeSessionBursts(t *testing.T) {
	adapter := &bulkDiscoverySource{}
	for index := range 40 {
		adapter.sessions = append(adapter.sessions, protocol.Session{
			ID: fmt.Sprintf("claude:%d", index), Kind: protocol.KindClaude,
			NativeID: fmt.Sprint(index), Name: "before", State: protocol.StateIdle,
		})
	}
	registry := source.NewRegistry()
	registry.Add(adapter)
	sink := &recordingSink{}
	agent := New(registry, sink)
	agent.refresh(context.Background(), true)
	for index := range adapter.sessions {
		adapter.sessions[index].Name = "after"
	}
	agent.refresh(context.Background(), false)

	sink.mu.Lock()
	events := append([]protocol.Event(nil), sink.events...)
	sink.mu.Unlock()
	var lists, updates int
	for _, event := range events {
		switch event.Type {
		case protocol.EvtSessions:
			lists++
		case protocol.EvtSessionUpdate:
			updates++
		}
	}
	if lists != 2 || updates != 0 {
		t.Fatalf("large refresh emitted %d lists and %d updates, want 2 and 0", lists, updates)
	}
}
