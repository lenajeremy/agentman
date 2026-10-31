package daemon

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lenajeremy/agentman/internal/attachments"
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

func (f *fakeStore) SaveData(_ string, data []byte) (string, error) {
	f.saved = append(f.saved, string(data))
	if f.err != nil {
		return "", f.err
	}
	return "/tmp/inline-" + string(data) + ".png", nil
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

// The Mac app sends its images with the message; they reach the agent as the
// phone's do, as paths before the text.
func TestImagesFromTheMacReachTheAgent(t *testing.T) {
	src := &injectingSource{}
	agent, _ := agentWithSource(t, src)
	store := &fakeStore{}
	agent.SetAttachments(store)

	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:s1", ClientID: "c1", Text: "why is this cut off?",
		Images: []protocol.InlineImage{
			{MIME: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("one"))},
			{MIME: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("two"))},
		},
	})
	if event.Status != protocol.StatusDelivered {
		t.Fatalf("send: %+v", event)
	}
	if got, want := src.injected(), "/tmp/inline-one.png /tmp/inline-two.png why is this cut off?"; got != want {
		t.Errorf("typed %q, want %q", got, want)
	}

	// An image alone is a message too.
	event = agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:s1", ClientID: "c2",
		Images: []protocol.InlineImage{{MIME: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("three"))}},
	})
	if event.Status != protocol.StatusDelivered || src.injected() != "/tmp/inline-three.png" {
		t.Fatalf("image alone: %+v, typed %q", event, src.injected())
	}
}

func TestMoreThanFourImagesFromTheMacAreRefused(t *testing.T) {
	src := &injectingSource{}
	agent, _ := agentWithSource(t, src)
	agent.SetAttachments(&fakeStore{})
	images := make([]protocol.InlineImage, 5)
	for i := range images {
		images[i] = protocol.InlineImage{MIME: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("x"))}
	}
	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:s1", ClientID: "c1", Images: images,
	})
	if event.Type != protocol.EvtError && event.Status != protocol.StatusFailed {
		t.Fatalf("five images were accepted: %+v", event)
	}
	if src.injected() != "" {
		t.Errorf("typed %q", src.injected())
	}
}

// The real store keeps only what is an image, under a name of its own.
func TestTheStoreFilesOnlyImages(t *testing.T) {
	dir := t.TempDir()
	store := attachments.New(dir, "", "")
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")
	path, err := store.SaveData("claude:s1", png)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, filepath.Join(dir, "images")) || filepath.Ext(path) != ".png" {
		t.Errorf("saved at %s", path)
	}
	if data, _ := os.ReadFile(path); len(data) != len(png) {
		t.Errorf("wrote %d bytes", len(data))
	}
	if _, err := store.SaveData("claude:s1", []byte("#!/bin/sh\necho hi")); err == nil {
		t.Error("a script was filed as an image")
	}
}

// recordingManager keeps the last request Agentman was handed.
type recordingManager struct{ req protocol.Request }

func (m *recordingManager) Handles(kind protocol.RequestType) bool {
	return kind == protocol.ReqManagerSend
}
func (m *recordingManager) Handle(_ context.Context, req protocol.Request) protocol.Event {
	m.req = req
	return protocol.Event{Type: protocol.EvtManagerEntry}
}
func (m *recordingManager) TurnComplete(protocol.Session)      {}
func (m *recordingManager) NeedsAnswer(protocol.Session)       {}
func (m *recordingManager) SessionsChanged([]protocol.Session) {}
func (m *recordingManager) On() bool                           { return false }

// A phone's images for Agentman come through the relay like an agent's, and
// Agentman is handed their bytes, with no file left behind.
func TestAPhonesImagesReachAgentmanAsBytes(t *testing.T) {
	dir := t.TempDir()
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	path := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(path, png, 0o600); err != nil {
		t.Fatal(err)
	}
	agent, _ := agentWithSource(t, &injectingSource{})
	agent.SetAttachments(&fakeStore{paths: map[string]string{"TICKET1": path}})
	boss := &recordingManager{}
	agent.SetManager(boss)

	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqManagerSend, Text: "what's on this screen?", UploadIDs: []string{"TICKET1"},
	})
	if event.Type == protocol.EvtError {
		t.Fatalf("send failed: %s", event.Error)
	}
	if len(boss.req.Images) != 1 || boss.req.Images[0].MIME != "image/png" ||
		boss.req.Images[0].Data != base64.StdEncoding.EncodeToString(png) || len(boss.req.UploadIDs) != 0 {
		t.Fatalf("Agentman was handed %+v", boss.req)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the collected image was left on disk")
	}

	too := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqManagerSend, Text: "x", UploadIDs: []string{"a", "b", "c", "d", "e"},
	})
	if too.Type != protocol.EvtError || !strings.Contains(too.Error, "at most") {
		t.Fatalf("five images were taken: %+v", too)
	}
}

// skillfulSource lists one skill, as a Claude with it installed does.
type skillfulSource struct{ injectingSource }

func (s *skillfulSource) Commands(context.Context, string) ([]protocol.Command, error) {
	return []protocol.Command{{Name: "compact"}, {Name: "frontend-design", Skill: true}}, nil
}

func (s *skillfulSource) CommandPanel(context.Context, string, string) (protocol.CommandPanel, error) {
	return protocol.CommandPanel{}, errors.New("none")
}

// A skill named mid-sentence reaches the agent in its own form: for
// Claude, with a space after it, which Claude needs to point the model at
// the skill.
func TestASkillNamedMidSentenceIsSentInTheAgentsForm(t *testing.T) {
	src := &skillfulSource{}
	agent, _ := agentWithSource(t, src)
	agent.refresh(context.Background(), true)
	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:s1", ClientID: "c1",
		Text: "Use /frontend-design, then ship it. Not /compact.",
	})
	if event.Type == protocol.EvtError {
		t.Fatalf("send: %s", event.Error)
	}
	if got := src.injected(); got != "Use /frontend-design , then ship it. Not /compact." {
		t.Errorf("typed %q", got)
	}
}
