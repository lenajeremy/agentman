package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// appearingSource reports its session only once it has been "reopened".
type appearingSource struct {
	streamingSource
	liveMu sync.Mutex
	live   bool
}

func (s *appearingSource) setLive(live bool) {
	s.liveMu.Lock()
	s.live = live
	s.liveMu.Unlock()
}

func (s *appearingSource) Discover(context.Context) ([]protocol.Session, error) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	if !s.live {
		return nil, nil
	}
	return []protocol.Session{{ID: "claude:s1", Kind: protocol.KindClaude, Name: "s1", Inject: protocol.InjectTmux}}, nil
}

// Opening an ended session subscribes to it before it is running. Reopening it
// keeps its id, so the phone has no reason to subscribe again, and the live
// tail used to never start: a reply sent after the resume never appeared.
func TestASubscriptionMadeBeforeAResumeStreamsOnceTheSessionIsLive(t *testing.T) {
	src := &appearingSource{}
	registry := source.NewRegistry()
	registry.Add(src)
	sink := &recordingSink{}
	agent := New(registry, sink)
	ctx := context.Background()
	agent.refresh(ctx, true)

	agent.HandleFrom(ctx, "phone", protocol.Request{Type: protocol.ReqSubscribe, SessionID: "claude:s1"})

	src.setLive(true)
	agent.refresh(ctx, false)
	time.Sleep(200 * time.Millisecond)
	if sink.messageCount() == 0 {
		t.Fatal("the reopened session streamed nothing to the phone that had it open")
	}

	// Leaving the screen still ends the tail it started.
	agent.HandleFrom(ctx, "phone", protocol.Request{Type: protocol.ReqUnsubscribe, SessionID: "claude:s1"})
	time.Sleep(100 * time.Millisecond)
	if got := src.activeFollows(); got != 0 {
		t.Errorf("%d tails still running after the phone left", got)
	}
}

// A phone that leaves before the session comes back must not leave a tail
// waiting to start for nobody.
func TestLeavingBeforeAResumeCancelsTheWaitingSubscription(t *testing.T) {
	for name, leave := range map[string]func(*Daemon){
		"unsubscribe": func(d *Daemon) {
			d.HandleFrom(context.Background(), "phone", protocol.Request{Type: protocol.ReqUnsubscribe, SessionID: "claude:s1"})
		},
		"disconnect": func(d *Daemon) { d.DisconnectSubscriber("phone") },
	} {
		src := &appearingSource{}
		registry := source.NewRegistry()
		registry.Add(src)
		agent := New(registry, &recordingSink{})
		ctx := context.Background()
		agent.refresh(ctx, true)
		agent.HandleFrom(ctx, "phone", protocol.Request{Type: protocol.ReqSubscribe, SessionID: "claude:s1"})
		leave(agent)

		src.setLive(true)
		agent.refresh(ctx, false)
		time.Sleep(100 * time.Millisecond)
		if got := src.activeFollows(); got != 0 {
			t.Errorf("%s: a tail started for a phone that had already left", name)
		}
	}
}
