package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
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
