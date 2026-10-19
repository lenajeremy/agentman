package daemon

import (
	"context"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

type queueSendingSource struct {
	streamingSource
	sent []string
}

func (s *queueSendingSource) SendQueued(_ context.Context, sessionID string) error {
	s.mu.Lock()
	s.sent = append(s.sent, sessionID)
	s.mu.Unlock()
	return nil
}

func TestSendQueuedRequestReachesSource(t *testing.T) {
	registry := source.NewRegistry()
	controlled := &queueSendingSource{}
	registry.Add(controlled)
	agent := New(registry, &recordingSink{})

	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqSendQueued, SessionID: "claude:s1", ClientID: "now-1",
	})
	if event.Type != protocol.EvtSendResult || event.Status != protocol.StatusDelivered || event.ClientID != "now-1" {
		t.Fatalf("send_queued result = %+v", event)
	}
	controlled.mu.Lock()
	sent := controlled.sent
	controlled.mu.Unlock()
	if len(sent) != 1 || sent[0] != "claude:s1" {
		t.Fatalf("source saw %q", sent)
	}
}

func TestSendQueuedFailsForAnAgentWithoutAQueue(t *testing.T) {
	registry := source.NewRegistry()
	registry.Add(&streamingSource{})
	agent := New(registry, &recordingSink{})

	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqSendQueued, SessionID: "claude:s1", ClientID: "now-2",
	})
	if event.Type != protocol.EvtSendResult || event.Status != protocol.StatusFailed || event.Error == "" {
		t.Fatalf("send_queued result = %+v", event)
	}
	missing := agent.Handle(context.Background(), protocol.Request{Type: protocol.ReqSendQueued, ClientID: "now-3"})
	if missing.Status != protocol.StatusFailed {
		t.Fatalf("send_queued without a session = %+v", missing)
	}
}
