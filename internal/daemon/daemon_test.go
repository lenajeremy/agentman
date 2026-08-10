package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

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

	for range 4 {
		agent.Handle(ctx, protocol.Request{Type: protocol.ReqSubscribe, SessionID: "claude:s1"})
	}
	time.Sleep(80 * time.Millisecond)

	if got := streaming.activeFollows(); got != 1 {
		t.Errorf("%d tails running for one session, want 1", got)
	}
}
