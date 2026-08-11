package source

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// ocFake is a stand-in for `opencode serve` that returns a scripted message
// list, so the streaming behaviour can be driven exactly.
type ocFake struct {
	mu       sync.Mutex
	messages []map[string]any
}

func (f *ocFake) set(messages []map[string]any) {
	f.mu.Lock()
	f.messages = messages
	f.mu.Unlock()
}

func (f *ocFake) start(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"healthy": true})
	})
	mux.HandleFunc("/api/session", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{
			"id":       "ses_1",
			"title":    "checkout",
			"location": map[string]any{"directory": "/Users/me/code"},
			"time":     map[string]any{"created": time.Now().UnixMilli(), "updated": time.Now().UnixMilli()},
		}}})
	})
	mux.HandleFunc("/api/session/active", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
	})
	mux.HandleFunc("/api/session/ses_1/message", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		// The real server returns newest first.
		reversed := make([]map[string]any, 0, len(f.messages))
		for i := len(f.messages) - 1; i >= 0; i-- {
			reversed = append(reversed, f.messages[i])
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data":   reversed,
			"cursor": map[string]any{"previous": "", "next": ""},
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func assistantMessage(id, partID, text string) map[string]any {
	return map[string]any{
		"id":   id,
		"type": "assistant",
		"time": map[string]any{"created": time.Now().UnixMilli()},
		"content": []map[string]any{
			{"id": partID, "type": "text", "text": text},
		},
	}
}

func userMessage(id, text string) map[string]any {
	return map[string]any{
		"id":   id,
		"type": "user",
		"time": map[string]any{"created": time.Now().UnixMilli()},
		"text": text,
	}
}

// TestOpenCodeMessageIDsAreUniqueAcrossMessages is the regression test for the
// bug that made OpenCode look broken from the phone.
//
// OpenCode numbers content parts within their message, so the first text part
// of every assistant message is "text-0". Using that as the message id made
// every assistant reply in a session collide: the app merges by id and showed
// one assistant row for the whole conversation, and Follow treated every new
// reply as already sent and never streamed it.
func TestOpenCodeMessageIDsAreUniqueAcrossMessages(t *testing.T) {
	fake := &ocFake{}
	server := fake.start(t)
	fake.set([]map[string]any{
		userMessage("msg_u1", "hello"),
		assistantMessage("msg_a1", "text-0", "Hi there."),
		userMessage("msg_u2", "say PELICAN"),
		assistantMessage("msg_a2", "text-0", "PELICAN"),
	})

	src := NewOpenCodeSource(server.URL)
	ctx := context.Background()
	if _, err := src.Discover(ctx); err != nil {
		t.Fatal(err)
	}

	page, err := src.Page(ctx, "opencode:ses_1", "", 20)
	if err != nil {
		t.Fatal(err)
	}

	ids := map[string]string{}
	for _, message := range page.Messages {
		if previous, clash := ids[message.ID]; clash {
			t.Fatalf("two messages share id %q (%q and %q) — the app merges by id, "+
				"so one would overwrite the other and the reply would never appear",
				message.ID, previous, message.Text)
		}
		ids[message.ID] = message.Text
	}

	if len(page.Messages) != 4 {
		t.Errorf("got %d messages, want 4: %+v", len(page.Messages), page.Messages)
	}
}

// TestOpenCodeFollowStreamsEachReply covers the symptom directly: send a
// message, get an answer.
func TestOpenCodeFollowStreamsEachReply(t *testing.T) {
	fake := &ocFake{}
	server := fake.start(t)
	fake.set([]map[string]any{
		userMessage("msg_u1", "hello"),
		assistantMessage("msg_a1", "text-0", "Hi there."),
	})

	src := NewOpenCodeSource(server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := src.Discover(ctx); err != nil {
		t.Fatal(err)
	}

	out := make(chan []protocol.Message, 16)
	go func() { _ = src.Follow(ctx, "opencode:ses_1", out) }()

	// Let Follow prime itself on the backlog before anything new arrives.
	time.Sleep(followInterval + 200*time.Millisecond)

	fake.set([]map[string]any{
		userMessage("msg_u1", "hello"),
		assistantMessage("msg_a1", "text-0", "Hi there."),
		userMessage("msg_u2", "say PELICAN"),
		assistantMessage("msg_a2", "text-0", "PELICAN"),
	})

	deadline := time.After(5 * time.Second)
	var got []protocol.Message
	for {
		select {
		case batch := <-out:
			got = append(got, batch...)
			for _, message := range got {
				if message.Text == "PELICAN" {
					return // the reply arrived, which is the whole point
				}
			}
		case <-deadline:
			t.Fatalf("the agent's reply never streamed; Follow emitted %+v", got)
		}
	}
}

// TestOpenCodeFollowSendsGrowingReplies covers the other half of the same bug.
//
// OpenCode fills a message in as the model produces it, so a reply appears as a
// fragment under an id and grows under that same id. Treating a known id as
// nothing-to-report left every long answer frozen at its first few words.
func TestOpenCodeFollowSendsGrowingReplies(t *testing.T) {
	fake := &ocFake{}
	server := fake.start(t)
	fake.set([]map[string]any{userMessage("msg_u1", "write a haiku")})

	src := NewOpenCodeSource(server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := src.Discover(ctx); err != nil {
		t.Fatal(err)
	}

	out := make(chan []protocol.Message, 16)
	go func() { _ = src.Follow(ctx, "opencode:ses_1", out) }()
	time.Sleep(followInterval + 200*time.Millisecond)

	fake.set([]map[string]any{
		userMessage("msg_u1", "write a haiku"),
		assistantMessage("msg_a1", "text-0", "An old silent pond"),
	})
	time.Sleep(followInterval + 400*time.Millisecond)

	fake.set([]map[string]any{
		userMessage("msg_u1", "write a haiku"),
		assistantMessage("msg_a1", "text-0", "An old silent pond / A frog jumps into the pond"),
	})

	deadline := time.After(5 * time.Second)
	for {
		select {
		case batch := <-out:
			for _, message := range batch {
				if strings.Contains(message.Text, "A frog jumps") {
					return
				}
			}
		case <-deadline:
			t.Fatal("the reply stopped updating once its id had been seen, " +
				"so a long answer would stay truncated on the phone")
		}
	}
}
