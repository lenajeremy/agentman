package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// replies collects what a dispatch answers, by request.
type replies struct {
	mu     sync.Mutex
	events map[string]protocol.Event
}

func (r *replies) add(replyTo string, event protocol.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.events == nil {
		r.events = map[string]protocol.Event{}
	}
	r.events[replyTo] = event
}

func (r *replies) get(replyTo string) (protocol.Event, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	event, ok := r.events[replyTo]
	return event, ok
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if done() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A send stuck in the agent's terminal, and the phone sending more, used to
// take every slot: listing a folder then failed with "too many requests"
// until the daemon was restarted. Reads now have slots of their own.
func TestAStuckSendLeavesReadsWorking(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	handler := func(ctx context.Context, _ string, req protocol.Request) protocol.Event {
		if req.Type == protocol.ReqSendMessage {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return protocol.Event{Type: protocol.EvtSendResult}
		}
		return protocol.Event{Type: protocol.EvtWorkspace}
	}
	var got replies
	d := newRequestDispatch(ctx, handler, got.add)

	// The first send sticks; every slot for actions fills behind it.
	for i := range maxQueuedActions {
		d.dispatch("phone", "send-"+string(rune('a'+i)), protocol.Request{Type: protocol.ReqSendMessage, SessionID: "antigravity:s"})
	}
	d.dispatch("phone", "send-over", protocol.Request{Type: protocol.ReqSendMessage, SessionID: "antigravity:s"})
	over, ok := got.get("send-over")
	if !ok || over.Type != protocol.EvtError || !strings.Contains(over.Error, "waiting to reach the agent") {
		t.Fatalf("one send too many = %+v, %v", over, ok)
	}

	// Reads still answer.
	for i := range maxConcurrentRequests {
		id := "read-" + string(rune('a'+i))
		d.dispatch("phone", id, protocol.Request{Type: protocol.ReqListFiles, SessionID: "antigravity:s"})
		waitFor(t, id, func() bool { event, ok := got.get(id); return ok && event.Type == protocol.EvtWorkspace })
	}

	close(release)
	waitFor(t, "the sends to finish", func() bool {
		_, ok := got.get("send-" + string(rune('a'+maxQueuedActions-1)))
		return ok
	})
	cancel()
	d.close()
}

// A request runs to a deadline, and one that is slow is logged by name.
func TestARequestHasADeadlineAndASlowOneIsLogged(t *testing.T) {
	var logged []string
	var mu sync.Mutex
	Logf = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, strings.TrimSpace(format))
	}
	defer func() { Logf = nil }()

	var deadline time.Time
	handler := func(ctx context.Context, _ string, req protocol.Request) protocol.Event {
		deadline, _ = ctx.Deadline()
		return protocol.Event{Type: protocol.EvtWorkspace}
	}
	d := &requestDispatch{handler: handler}
	d.handle(context.Background(), "phone", protocol.Request{Type: protocol.ReqListFiles})
	if until := time.Until(deadline); until <= 0 || until > requestDeadline {
		t.Errorf("deadline in %s, want within %s", until, requestDeadline)
	}
	mu.Lock()
	quiet := len(logged)
	mu.Unlock()
	if quiet != 0 {
		t.Errorf("a quick request was logged: %v", logged)
	}
}
