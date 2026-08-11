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

// ocFake is a stand-in for OpenCode's published HTTP API. Its response shapes
// are copied from the OpenAPI document served by OpenCode 1.18.15 at /doc.
type ocFake struct {
	mu          sync.Mutex
	sessionID   string
	title       string
	directory   string
	messages    []map[string]any
	statuses    map[string]any
	questions   []map[string]any
	permissions []map[string]any
	cursor      string
	requests    []ocCapturedRequest
}

type ocCapturedRequest struct {
	method string
	path   string
	query  string
	body   map[string]any
}

func (f *ocFake) set(messages []map[string]any) {
	f.mu.Lock()
	f.messages = messages
	f.mu.Unlock()
}

func (f *ocFake) id() string {
	if f.sessionID != "" {
		return f.sessionID
	}
	return "ses_1"
}

func (f *ocFake) dir() string {
	if f.directory != "" {
		return f.directory
	}
	return "/Users/me/code"
}

func (f *ocFake) capture(r *http.Request) {
	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	f.mu.Lock()
	f.requests = append(f.requests, ocCapturedRequest{
		method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: body,
	})
	f.mu.Unlock()
}

func (f *ocFake) lastRequest() ocCapturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return ocCapturedRequest{}
	}
	return f.requests[len(f.requests)-1]
}

func (f *ocFake) start(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"healthy": true})
	})
	mux.HandleFunc("/session", func(w http.ResponseWriter, _ *http.Request) {
		title := f.title
		if title == "" {
			title = "checkout"
		}
		json.NewEncoder(w).Encode([]map[string]any{{
			"id": f.id(), "slug": "checkout", "projectID": "project-1",
			"directory": f.dir(), "title": title, "version": "1.18.15",
			"model": map[string]any{"id": "big-pickle", "providerID": "opencode"},
			"time":  map[string]any{"created": time.Now().UnixMilli(), "updated": time.Now().UnixMilli()},
		}})
	})
	mux.HandleFunc("/session/status", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.statuses == nil {
			f.statuses = map[string]any{}
		}
		json.NewEncoder(w).Encode(f.statuses)
	})
	mux.HandleFunc("/question", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(f.questions)
	})
	mux.HandleFunc("/permission", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(f.permissions)
	})
	mux.HandleFunc("/session/"+f.id()+"/message", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.cursor != "" {
			w.Header().Set("X-Next-Cursor", f.cursor)
		}
		json.NewEncoder(w).Encode(f.messages)
	})
	mux.HandleFunc("/session/"+f.id()+"/prompt_async", func(w http.ResponseWriter, r *http.Request) {
		f.capture(r)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/session/"+f.id()+"/abort", func(w http.ResponseWriter, r *http.Request) {
		f.capture(r)
		json.NewEncoder(w).Encode(true)
	})
	mux.HandleFunc("/question/que_1/reply", func(w http.ResponseWriter, r *http.Request) {
		f.capture(r)
		json.NewEncoder(w).Encode(true)
	})
	mux.HandleFunc("/permission/per_1/reply", func(w http.ResponseWriter, r *http.Request) {
		f.capture(r)
		json.NewEncoder(w).Encode(true)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func assistantMessage(id, partID, text string) map[string]any {
	return map[string]any{
		"info": map[string]any{
			"id": id, "role": "assistant", "modelID": "big-pickle",
			"providerID": "opencode", "time": map[string]any{"created": time.Now().UnixMilli()},
		},
		"parts": []map[string]any{
			{"id": partID, "type": "text", "text": text},
		},
	}
}

func userMessage(id, text string) map[string]any {
	return map[string]any{
		"info": map[string]any{
			"id": id, "role": "user", "time": map[string]any{"created": time.Now().UnixMilli()},
		},
		"parts": []map[string]any{{"id": id + "-text", "type": "text", "text": text}},
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

func TestOpenCodePageUsesHeaderCursor(t *testing.T) {
	fake := &ocFake{cursor: "older page/+="}
	server := fake.start(t)
	fake.set([]map[string]any{userMessage("msg_u1", "hello")})

	src := NewOpenCodeSource(server.URL)
	ctx := context.Background()
	if _, err := src.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := src.Page(ctx, "opencode:ses_1", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	// Page must echo the opaque header exactly, not reinterpret it.
	if !page.HasMore || page.NextCursor != fake.cursor {
		t.Fatalf("cursor = %q (hasMore %v), want %q", page.NextCursor, page.HasMore, fake.cursor)
	}
}

func TestOpenCodeInjectUsesAsyncPromptAPI(t *testing.T) {
	fake := &ocFake{}
	server := fake.start(t)
	src := NewOpenCodeSource(server.URL)
	ctx := context.Background()
	if _, err := src.Discover(ctx); err != nil {
		t.Fatal(err)
	}

	mode, err := src.Inject(ctx, "opencode:ses_1", "run the tests")
	if err != nil {
		t.Fatal(err)
	}
	if mode != protocol.InjectAPI {
		t.Fatalf("mode = %q, want api", mode)
	}
	request := fake.lastRequest()
	if request.method != http.MethodPost || request.path != "/session/ses_1/prompt_async" {
		t.Fatalf("sent %s %s, want POST /session/ses_1/prompt_async", request.method, request.path)
	}
	parts, ok := request.body["parts"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("prompt body = %#v, want one text part", request.body)
	}
	part, _ := parts[0].(map[string]any)
	if part["type"] != "text" || part["text"] != "run the tests" {
		t.Fatalf("prompt part = %#v", part)
	}
}

func TestOpenCodeQuestionCanBeAnswered(t *testing.T) {
	fake := &ocFake{questions: []map[string]any{{
		"id": "que_1", "sessionID": "ses_1",
		"questions": []map[string]any{{
			"header": "Database", "question": "Which database?",
			"options": []map[string]any{{"label": "Postgres"}, {"label": "SQLite"}},
		}},
	}}}
	server := fake.start(t)
	src := NewOpenCodeSource(server.URL)
	ctx := context.Background()
	sessions, err := src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].State != protocol.StateWaitingInput || sessions[0].Question == nil {
		t.Fatalf("pending question was not discovered: %+v", sessions)
	}
	if err := src.Answer(ctx, "opencode:ses_1", "Postgres"); err != nil {
		t.Fatal(err)
	}
	request := fake.lastRequest()
	if request.path != "/question/que_1/reply" {
		t.Fatalf("reply path = %q", request.path)
	}
	answers, ok := request.body["answers"].([]any)
	if !ok || len(answers) != 1 {
		t.Fatalf("reply body = %#v", request.body)
	}
}

func TestOpenCodePermissionCanBeAnswered(t *testing.T) {
	fake := &ocFake{permissions: []map[string]any{{
		"id": "per_1", "sessionID": "ses_1", "permission": "bash",
		"patterns": []string{"go test ./..."}, "always": []string{"go test *"},
		"metadata": map[string]any{},
	}}}
	server := fake.start(t)
	src := NewOpenCodeSource(server.URL)
	ctx := context.Background()
	sessions, err := src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Question == nil {
		t.Fatalf("pending permission was not discovered: %+v", sessions)
	}
	if sessions[0].Question.Detail != "go test ./..." {
		t.Errorf("permission detail = %q", sessions[0].Question.Detail)
	}
	if err := src.Answer(ctx, "opencode:ses_1", "once"); err != nil {
		t.Fatal(err)
	}
	request := fake.lastRequest()
	if request.path != "/permission/per_1/reply" || request.body["reply"] != "once" {
		t.Fatalf("permission reply = %#v", request)
	}
}

func TestOpenCodeInterruptUsesAbortAPI(t *testing.T) {
	fake := &ocFake{}
	server := fake.start(t)
	src := NewOpenCodeSource(server.URL)
	ctx := context.Background()
	if _, err := src.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := src.Interrupt(ctx, "opencode:ses_1"); err != nil {
		t.Fatal(err)
	}
	request := fake.lastRequest()
	if request.path != "/session/ses_1/abort" {
		t.Fatalf("interrupt path = %q, want /session/ses_1/abort", request.path)
	}
}

func TestOpenCodeDiscoversEveryLiveServer(t *testing.T) {
	first := &ocFake{sessionID: "ses_1", directory: "/Users/me/one"}
	second := &ocFake{sessionID: "ses_2", directory: "/Users/me/two"}
	firstServer := first.start(t)
	secondServer := second.start(t)

	src := NewOpenCodeSource(firstServer.URL)
	src.findServers = func(context.Context) []string {
		return []string{firstServer.URL, secondServer.URL}
	}
	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want one from each live server: %+v", len(sessions), sessions)
	}
}

func TestOpenCodeUsesConfiguredServerUsername(t *testing.T) {
	t.Setenv("OPENCODE_SERVER_USERNAME", "agentman-test")
	t.Setenv("OPENCODE_SERVER_PASSWORD", "secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "agentman-test" || password != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"healthy": true})
	}))
	defer server.Close()

	src := NewOpenCodeSource(server.URL)
	if !src.Available(context.Background()) {
		t.Fatal("server rejected authentication; configured OpenCode username was not used")
	}
}
