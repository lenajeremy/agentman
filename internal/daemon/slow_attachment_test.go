package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// interruptibleSource can be typed into and stopped.
type interruptibleSource struct {
	injectingSource
	interrupted chan struct{}
}

func (s *interruptibleSource) Interrupt(context.Context, string) error {
	close(s.interrupted)
	return nil
}

// slowStore holds an image download open until released.
type slowStore struct {
	started, release chan struct{}
}

func (s *slowStore) Save(ctx context.Context, _, _ string) (string, error) {
	close(s.started)
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	return "/tmp/image.png", nil
}

// Collecting a message's images from the relay can take up to 30 seconds
// each. It happened while the session's action lock was held, so tapping
// Stop on that session waited behind the download.
func TestStopIsNotHeldUpByAnImageStillDownloading(t *testing.T) {
	src := &interruptibleSource{interrupted: make(chan struct{})}
	agent, _ := agentWithSource(t, src)
	agent.refresh(context.Background(), true)
	store := &slowStore{started: make(chan struct{}), release: make(chan struct{})}
	agent.SetAttachments(store)
	defer close(store.release)

	go agent.HandleFrom(context.Background(), "phone", protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:s1", ClientID: "send-1",
		Text: "look at this", UploadIDs: []string{"ABCDEFGHIJKLMNOP"},
	})
	<-store.started

	stopped := make(chan protocol.Event, 1)
	go func() {
		stopped <- agent.HandleFrom(context.Background(), "phone", protocol.Request{
			Type: protocol.ReqInterrupt, SessionID: "claude:s1", ClientID: "stop-1",
		})
	}()
	select {
	case <-src.interrupted:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop waited for the image download")
	}
	if event := <-stopped; event.Status != protocol.StatusDelivered {
		t.Errorf("Stop answered %+v", event)
	}
}

// The relay connection runs actions one at a time, in order. Stop is the
// exception: it must not queue behind a slow send to any session.
func TestStopDoesNotQueueBehindOtherActions(t *testing.T) {
	if queuedInOrder(protocol.ReqInterrupt) {
		t.Error("Stop waits in the ordered queue")
	}
	for _, kind := range []protocol.RequestType{protocol.ReqSendMessage, protocol.ReqAnswer, protocol.ReqResumeSession} {
		if !queuedInOrder(kind) {
			t.Errorf("%s no longer runs in arrival order", kind)
		}
	}
}
