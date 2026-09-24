package daemon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// injectingSource records the text that would have been typed into the agent.
type injectingSource struct {
	streamingSource
	mu   sync.Mutex
	text string
}

func (s *injectingSource) Inject(_ context.Context, _, text string) (protocol.InjectMode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.text = text
	return protocol.InjectTmux, nil
}

func (s *injectingSource) injected() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.text
}

type fakeStore struct {
	paths map[string]string
	err   error
	saved []string
}

func (f *fakeStore) Save(_ context.Context, _, uploadID string) (string, error) {
	f.saved = append(f.saved, uploadID)
	if f.err != nil {
		return "", f.err
	}
	return f.paths[uploadID], nil
}

func agentWithSource(t *testing.T, src source.Source) (*Daemon, *recordingSink) {
	t.Helper()
	registry := source.NewRegistry()
	registry.Add(src)
	sink := &recordingSink{}
	return New(registry, sink), sink
}

// The agent is handed a path because it cannot be handed bytes: the daemon
// drives it by typing, so an image has to become something typeable.
func TestSentImagesBecomePathsInTheTypedMessage(t *testing.T) {
	src := &injectingSource{}
	agent, _ := agentWithSource(t, src)
	agent.SetAttachments(&fakeStore{paths: map[string]string{
		"TICKET1": "/Users/mac/.agentman/images/claude-s1/aaa.png",
		"TICKET2": "/Users/mac/.agentman/images/claude-s1/bbb.jpg",
	}})

	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:s1", ClientID: "c1",
		Text: "what is wrong with this layout?", UploadIDs: []string{"TICKET1", "TICKET2"},
	})
	if event.Status != protocol.StatusDelivered {
		t.Fatalf("send: %+v", event)
	}
	got := src.injected()
	want := "/Users/mac/.agentman/images/claude-s1/aaa.png /Users/mac/.agentman/images/claude-s1/bbb.jpg what is wrong with this layout?"
	if got != want {
		t.Errorf("typed %q,\n want %q", got, want)
	}
	// One line, or the newline submits the message before the text arrives.
	if strings.ContainsAny(got, "\n\r") {
		t.Error("the composed message would submit early")
	}
}

// "Look at this" is a real message.
func TestAnImageWithNoTextIsStillAMessage(t *testing.T) {
	src := &injectingSource{}
	agent, _ := agentWithSource(t, src)
	agent.SetAttachments(&fakeStore{paths: map[string]string{"TICKET1": "/tmp/a.png"}})

	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:s1", ClientID: "c1",
		UploadIDs: []string{"TICKET1"},
	})
	if event.Status != protocol.StatusDelivered {
		t.Fatalf("send: %+v", event)
	}
	if src.injected() != "/tmp/a.png" {
		t.Errorf("typed %q", src.injected())
	}
}

// All or nothing: a message that mentions two screenshots and arrives with one
// would have the agent answering about the wrong picture.
func TestAFailedImageFailsTheWholeMessage(t *testing.T) {
	src := &injectingSource{}
	agent, _ := agentWithSource(t, src)
	agent.SetAttachments(&fakeStore{err: errors.New("that image expired before it could be collected")})

	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:s1", ClientID: "c1",
		Text: "look", UploadIDs: []string{"TICKET1"},
	})
	if event.Status != protocol.StatusFailed {
		t.Fatalf("send: %+v", event)
	}
	if !strings.Contains(event.Error, "expired") {
		t.Errorf("error = %q", event.Error)
	}
	if src.injected() != "" {
		t.Errorf("a message was typed anyway: %q", src.injected())
	}
}

func TestImagesNeedARelayToArriveThrough(t *testing.T) {
	src := &injectingSource{}
	agent, _ := agentWithSource(t, src)

	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:s1", ClientID: "c1",
		Text: "look", UploadIDs: []string{"TICKET1"},
	})
	if event.Status != protocol.StatusFailed || !strings.Contains(event.Error, "relay") {
		t.Fatalf("send without a relay: %+v", event)
	}
}

func TestTooManyImagesOnOneMessageIsRejected(t *testing.T) {
	agent, _ := agentWithSource(t, &injectingSource{})
	store := &fakeStore{paths: map[string]string{}}
	agent.SetAttachments(store)

	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:s1", ClientID: "c1",
		Text: "look", UploadIDs: []string{"A", "B", "C", "D", "E"},
	})
	if event.Status != protocol.StatusFailed {
		t.Fatalf("send: %+v", event)
	}
	if len(store.saved) != 0 {
		t.Error("the relay was contacted before the request was validated")
	}
}

func TestAMessageWithNeitherTextNorImagesIsStillRefused(t *testing.T) {
	agent, _ := agentWithSource(t, &injectingSource{})
	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:s1", ClientID: "c1", Text: "   ",
	})
	if event.Status != protocol.StatusFailed {
		t.Fatalf("send: %+v", event)
	}
}
