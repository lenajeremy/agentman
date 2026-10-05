package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// structuredSource takes images as parts of one message, the way Cursor's ACP
// channel does, and can decline a session that has no such channel.
type structuredSource struct {
	injectingSource
	decline    bool
	structured []string
}

func (s *structuredSource) InjectWithAttachments(
	_ context.Context, _, text string, paths []string,
) (protocol.InjectMode, error) {
	if s.decline {
		return protocol.InjectNone, source.ErrAttachmentsAsPaths
	}
	s.structured = append([]string{text}, paths...)
	return protocol.InjectAPI, nil
}

// placingSource reads images from a place of its own, the way Antigravity
// reads its conversation's .user_uploaded directory.
type placingSource struct {
	injectingSource
	place func(path string) (string, error)
}

func (s *placingSource) PlaceAttachment(_ context.Context, _, path string) (string, error) {
	return s.place(path)
}

func sendWithImages(t *testing.T, src source.Source) protocol.Event {
	t.Helper()
	agent, _ := agentWithSource(t, src)
	agent.SetAttachments(&fakeStore{paths: map[string]string{
		"T1": "/Users/mac/.agentman/images/claude-s1/aaa.png",
		"T2": "/Users/mac/.agentman/images/claude-s1/bbb.png",
	}})
	return agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:s1", ClientID: "c1",
		Text: "compare these", UploadIDs: []string{"T1", "T2"},
	})
}

func TestStructuredImagesArePreferredToTypedPaths(t *testing.T) {
	src := &structuredSource{}
	event := sendWithImages(t, src)
	if event.Status != protocol.StatusDelivered {
		t.Fatalf("send = %+v", event)
	}
	want := []string{"compare these",
		"/Users/mac/.agentman/images/claude-s1/aaa.png", "/Users/mac/.agentman/images/claude-s1/bbb.png"}
	if strings.Join(src.structured, "|") != strings.Join(want, "|") {
		t.Fatalf("structured delivery got %q, want %q", src.structured, want)
	}
	if typed := src.injected(); typed != "" {
		t.Errorf("the paths were typed as well: %q", typed)
	}
}

// A terminal chat of an agent whose other sessions take structured images is
// typed the paths, exactly as any other terminal agent.
func TestASessionWithoutAStructuredChannelIsTypedThePaths(t *testing.T) {
	src := &structuredSource{decline: true}
	if event := sendWithImages(t, src); event.Status != protocol.StatusDelivered {
		t.Fatalf("send = %+v", event)
	}
	want := "/Users/mac/.agentman/images/claude-s1/aaa.png /Users/mac/.agentman/images/claude-s1/bbb.png compare these"
	if got := src.injected(); got != want {
		t.Fatalf("typed %q, want %q", got, want)
	}
}

func TestAMessageWithoutImagesNeverUsesTheStructuredChannel(t *testing.T) {
	src := &structuredSource{}
	agent, _ := agentWithSource(t, src)
	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqSendMessage, SessionID: "claude:s1", ClientID: "c1", Text: "hello",
	})
	if event.Status != protocol.StatusDelivered || src.injected() != "hello" || src.structured != nil {
		t.Fatalf("plain send = %+v, typed %q, structured %q", event, src.injected(), src.structured)
	}
}

func TestImagesAreTypedWhereTheAgentReadsThem(t *testing.T) {
	src := &placingSource{place: func(path string) (string, error) {
		return "/Users/mac/.gemini/brain/c1/.user_uploaded/" + path[strings.LastIndex(path, "/")+1:], nil
	}}
	if event := sendWithImages(t, src); event.Status != protocol.StatusDelivered {
		t.Fatalf("send = %+v", event)
	}
	want := "/Users/mac/.gemini/brain/c1/.user_uploaded/aaa.png /Users/mac/.gemini/brain/c1/.user_uploaded/bbb.png compare these"
	if got := src.injected(); got != want {
		t.Fatalf("typed %q, want %q", got, want)
	}
}

// Placing is a preference: when it fails, or answers with something that is
// not one absolute path on one line, the agent still gets the image.
func TestAFailedPlacementFallsBackToTheSavedPath(t *testing.T) {
	saved := "/Users/mac/.agentman/images/claude-s1/aaa.png /Users/mac/.agentman/images/claude-s1/bbb.png compare these"
	for name, place := range map[string]func(string) (string, error){
		"error":         func(string) (string, error) { return "", errors.New("conversation unknown") },
		"relative path": func(string) (string, error) { return "aaa.png", nil },
		"newline":       func(string) (string, error) { return "/brain/a\nb.png", nil },
		"tab":           func(string) (string, error) { return "/brain/a\tb.png", nil },
		"escape":        func(string) (string, error) { return "/brain/\x1b[2Ja.png", nil },
	} {
		t.Run(name, func(t *testing.T) {
			src := &placingSource{place: place}
			if event := sendWithImages(t, src); event.Status != protocol.StatusDelivered {
				t.Fatalf("send = %+v", event)
			}
			if got := src.injected(); got != saved {
				t.Fatalf("typed %q, want the saved paths %q", got, saved)
			}
		})
	}
}
