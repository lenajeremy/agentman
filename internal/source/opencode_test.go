package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

// ocFake is a stand-in for OpenCode's published HTTP API. Its response shapes
// are copied from the OpenAPI document served by OpenCode 1.18.15 at /doc.
type ocFake struct {
	mu           sync.Mutex
	sessionID    string
	title        string
	directory    string
	messages     []map[string]any
	statuses     map[string]any
	questions    []map[string]any
	permissions  []map[string]any
	cursor       string
	requests     []ocCapturedRequest
	failSession  bool
	scopePending bool
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

func (f *ocFake) setSessionFailure(fail bool) {
	f.mu.Lock()
	f.failSession = fail
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

func (f *ocFake) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *ocFake) start(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"healthy": true})
	})
	mux.HandleFunc("/session", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		fail := f.failSession
		f.mu.Unlock()
		if fail {
			http.Error(w, "temporary failure", http.StatusServiceUnavailable)
			return
		}
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
	mux.HandleFunc("/session/status", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.scopePending && r.URL.Query().Get("directory") != f.dir() {
			json.NewEncoder(w).Encode(map[string]any{})
			return
		}
		if f.statuses == nil {
			f.statuses = map[string]any{}
		}
		json.NewEncoder(w).Encode(f.statuses)
	})
	mux.HandleFunc("/question", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.scopePending && r.URL.Query().Get("directory") != f.dir() {
			json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		json.NewEncoder(w).Encode(f.questions)
	})
	mux.HandleFunc("/permission", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.scopePending && r.URL.Query().Get("directory") != f.dir() {
			json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
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

func TestOpenCodeFollowDedupStateIsBounded(t *testing.T) {
	seen := map[string]openCodeSeenMessage{}
	for generation := uint64(1); generation <= 1_000; generation++ {
		messages := make([]protocol.Message, 20)
		for index := range messages {
			messages[index] = protocol.Message{
				ID: fmt.Sprintf("message-%d-%d", generation, index), Text: "content",
			}
		}
		_ = updateOpenCodeSeen(seen, messages, generation)
	}
	if got := len(seen); got > 60 {
		t.Fatalf("follow retained %d fingerprints for a 20-message window", got)
	}
}

func TestOpenCodeSeenEmitsChangedMessageContent(t *testing.T) {
	seen := map[string]openCodeSeenMessage{}
	first := protocol.Message{ID: "message-1", Text: "partial"}
	if got := updateOpenCodeSeen(seen, []protocol.Message{first}, 1); len(got) != 1 {
		t.Fatalf("new message emitted %d rows", len(got))
	}
	if got := updateOpenCodeSeen(seen, []protocol.Message{first}, 2); len(got) != 0 {
		t.Fatalf("unchanged message emitted %d rows", len(got))
	}
	first.Text = "complete"
	if got := updateOpenCodeSeen(seen, []protocol.Message{first}, 3); len(got) != 1 {
		t.Fatalf("grown message emitted %d rows", len(got))
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
	questionID := sessions[0].Question.ID
	if questionID == "" {
		t.Fatal("pending question has no revision id")
	}
	before := fake.requestCount()
	if err := src.Answer(ctx, "opencode:ses_1", protocol.QuestionAnswer{
		OptionKey: "Postgres",
	}); err == nil {
		t.Fatal("answer without a question revision was accepted")
	}
	if err := src.Answer(ctx, "opencode:ses_1", protocol.QuestionAnswer{
		QuestionID: "question-older-request-0", OptionKey: "Postgres",
	}); err == nil {
		t.Fatal("stale question revision was accepted")
	}
	if got := fake.requestCount(); got != before {
		t.Fatalf("stale answer made %d API requests", got-before)
	}
	if err := src.Answer(ctx, "opencode:ses_1", protocol.QuestionAnswer{
		QuestionID: questionID, OptionKey: "Postgres",
	}); err != nil {
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

func TestOpenCodeScopesQuestionsToTheSessionDirectory(t *testing.T) {
	fake := &ocFake{
		directory:    "/Users/me/other-project",
		scopePending: true,
		questions: []map[string]any{{
			"id": "que_1", "sessionID": "ses_1",
			"questions": []map[string]any{{
				"header": "Database", "question": "Which database?",
				"options": []map[string]any{{"label": "Postgres"}, {"label": "SQLite"}},
			}},
		}},
	}
	server := fake.start(t)
	src := NewOpenCodeSource(server.URL)
	ctx := context.Background()

	sessions, err := src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].State != protocol.StateWaitingInput || sessions[0].Question == nil {
		t.Fatalf("directory-scoped question was not discovered: %+v", sessions)
	}
	if err := src.Answer(ctx, "opencode:ses_1", currentOpenCodeAnswer(
		t, src, "opencode:ses_1", protocol.QuestionAnswer{OptionKey: "Postgres"},
	)); err != nil {
		t.Fatalf("directory-scoped question could not be answered: %v", err)
	}
	if got := fake.lastRequest().query; !strings.Contains(got, "directory=%2FUsers%2Fme%2Fother-project") {
		t.Fatalf("reply query = %q, want the session directory", got)
	}
}

func TestOpenCodeScopesStatusToTheSessionDirectory(t *testing.T) {
	fake := &ocFake{
		directory:    "/Users/me/other-project",
		scopePending: true,
		statuses: map[string]any{
			"ses_1": map[string]any{"type": "busy"},
		},
	}
	server := fake.start(t)
	src := NewOpenCodeSource(server.URL)

	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].State != protocol.StateBusy {
		t.Fatalf("directory-scoped status was not discovered: %+v", sessions)
	}
}

func TestOpenCodeMultipleAndCustomQuestionCanBeAnswered(t *testing.T) {
	fake := &ocFake{questions: []map[string]any{{
		"id": "que_1", "sessionID": "ses_1",
		"questions": []map[string]any{{
			"header": "Targets", "question": "Which targets?", "multiple": true, "custom": true,
			"options": []map[string]any{{"label": "API"}, {"label": "CLI"}},
		}},
	}}}
	server := fake.start(t)
	src := NewOpenCodeSource(server.URL)
	ctx := context.Background()
	sessions, err := src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	question := sessions[0].Question
	if question == nil || !question.Multiple || !question.Custom {
		t.Fatalf("question capabilities were lost: %+v", question)
	}
	answer := currentOpenCodeAnswer(t, src, "opencode:ses_1", protocol.QuestionAnswer{
		Options: []string{"API", "CLI"}, Text: "Desktop",
	})
	if err := src.Answer(ctx, "opencode:ses_1", answer); err != nil {
		t.Fatal(err)
	}
	request := fake.lastRequest()
	answers, ok := request.body["answers"].([]any)
	if !ok || len(answers) != 1 {
		t.Fatalf("reply body = %#v", request.body)
	}
	values, ok := answers[0].([]any)
	if !ok || len(values) != 3 || values[0] != "API" || values[1] != "CLI" || values[2] != "Desktop" {
		t.Fatalf("multiple/custom answer = %#v", values)
	}
}

func TestOpenCodeDefaultsCustomTrueAndPreservesDescriptions(t *testing.T) {
	fake := &ocFake{questions: []map[string]any{{
		"id": "que_1", "sessionID": "ses_1",
		"questions": []map[string]any{
			{
				"header": "Single choice", "question": "Pick one",
				"options": []map[string]any{
					{"label": "Focused", "description": "Deep in a flow state"},
					{"label": "Chill", "description": "Taking it easy"},
				},
			},
			{
				"header": "Open-ended", "question": "What comes to mind?",
				"options": []map[string]any{},
			},
		},
	}}}
	server := fake.start(t)
	src := NewOpenCodeSource(server.URL)
	ctx := context.Background()

	sessions, err := src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first := sessions[0].Question
	if first == nil || !first.Custom {
		t.Fatalf("OpenCode's omitted custom flag did not default true: %+v", first)
	}
	if got := first.Options[0].Description; got != "Deep in a flow state" {
		t.Errorf("description = %q", got)
	}
	if err := src.Answer(ctx, "opencode:ses_1", currentOpenCodeAnswer(
		t, src, "opencode:ses_1", protocol.QuestionAnswer{OptionKey: "Focused"},
	)); err != nil {
		t.Fatal(err)
	}

	sessions, err = src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second := sessions[0].Question
	if second == nil || second.Prompt != "What comes to mind?" || !second.Custom || len(second.Options) != 0 {
		t.Fatalf("custom-only default question was hidden: %+v", second)
	}
	if err := src.Answer(ctx, "opencode:ses_1", currentOpenCodeAnswer(
		t, src, "opencode:ses_1", protocol.QuestionAnswer{Text: "Agentman"},
	)); err != nil {
		t.Fatal(err)
	}
	request := fake.lastRequest()
	answers, ok := request.body["answers"].([]any)
	if !ok || len(answers) != 2 {
		t.Fatalf("reply body = %#v", request.body)
	}
}

func TestOpenCodeExplicitlyDisablesCustomAnswers(t *testing.T) {
	fake := &ocFake{questions: []map[string]any{{
		"id": "que_1", "sessionID": "ses_1",
		"questions": []map[string]any{{
			"header": "Confirm", "question": "Continue?", "custom": false,
			"options": []map[string]any{{"label": "Yes"}, {"label": "No"}},
		}},
	}}}
	server := fake.start(t)
	src := NewOpenCodeSource(server.URL)
	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if question := sessions[0].Question; question == nil || question.Custom {
		t.Fatalf("explicit custom:false was ignored: %+v", question)
	}
}

// Set AGENTMAN_LIVE_OPENCODE_QUESTIONS to the native session id of a live
// OpenCode request containing the six-format compatibility matrix. This is
// intentionally opt-in because it answers the real request and lets the agent
// resume. It verifies the supported API rather than a reconstructed payload.
func TestLiveOpenCodeQuestionFormats(t *testing.T) {
	nativeID := os.Getenv("AGENTMAN_LIVE_OPENCODE_QUESTIONS")
	if nativeID == "" {
		t.Skip("no live OpenCode question session supplied")
	}
	src := NewOpenCodeSource("http://127.0.0.1:4096")
	ctx := context.Background()
	sessionID := "opencode:" + nativeID
	steps := []struct {
		promptContains string
		answer         protocol.QuestionAnswer
		check          func(*testing.T, *protocol.Question)
	}{
		{
			promptContains: "mood right now",
			answer:         protocol.QuestionAnswer{OptionKey: "Focused"},
			check: func(t *testing.T, question *protocol.Question) {
				if !question.Custom {
					t.Error("OpenCode's default custom answer was disabled")
				}
				if len(question.Options) != 4 || question.Options[0].Description != "Ready to work deeply." {
					t.Errorf("single-select options lost descriptions: %+v", question.Options)
				}
			},
		},
		{
			promptContains: "Which parts should we test?",
			answer: protocol.QuestionAnswer{Options: []string{
				"CLI feature", "Mobile app",
			}},
			check: func(t *testing.T, question *protocol.Question) {
				if !question.Multiple || !question.Custom {
					t.Errorf("multi-select capabilities = multiple %t custom %t", question.Multiple, question.Custom)
				}
			},
		},
		{
			promptContains: "workflow optimize for",
			answer:         protocol.QuestionAnswer{Text: "Reliable remote question forms"},
			check: func(t *testing.T, question *protocol.Question) {
				if !question.Custom || len(question.Options) != 0 {
					t.Errorf("custom-only question = %+v", question)
				}
			},
		},
		{
			promptContains: "tradeoff should win",
			answer:         protocol.QuestionAnswer{OptionKey: "Favor correctness"},
		},
		{
			promptContains: "confident are you",
			answer:         protocol.QuestionAnswer{OptionKey: "4"},
			check: func(t *testing.T, question *protocol.Question) {
				if len(question.Options) != 5 {
					t.Errorf("five-option scale was truncated: %+v", question.Options)
				}
			},
		},
		{
			promptContains: "Choose a note",
			answer:         protocol.QuestionAnswer{Text: "Keep the real API payloads as fixtures."},
		},
	}

	for index, step := range steps {
		sessions, err := src.Discover(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var current *protocol.Session
		for sessionIndex := range sessions {
			if sessions[sessionIndex].ID == sessionID {
				current = &sessions[sessionIndex]
				break
			}
		}
		if current == nil || current.Question == nil {
			t.Fatalf("step %d question missing: %+v", index+1, current)
		}
		if !strings.Contains(current.Question.Prompt, step.promptContains) {
			t.Fatalf("step %d prompt = %q, want text %q", index+1, current.Question.Prompt, step.promptContains)
		}
		if current.Question.Detail != fmt.Sprintf("Question %d of %d", index+1, len(steps)) {
			t.Errorf("step %d detail = %q", index+1, current.Question.Detail)
		}
		if step.check != nil {
			step.check(t, current.Question)
		}
		if err := src.Answer(ctx, sessionID, currentOpenCodeAnswer(t, src, sessionID, step.answer)); err != nil {
			t.Fatalf("step %d answer: %v", index+1, err)
		}
	}

	sessions, err := src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.ID == sessionID && session.Question != nil {
			t.Fatalf("OpenCode request remained pending after all six answers: %+v", session.Question)
		}
	}
}

func TestOpenCodeCustomOnlyQuestionIsNotHidden(t *testing.T) {
	fake := &ocFake{questions: []map[string]any{{
		"id": "que_1", "sessionID": "ses_1",
		"questions": []map[string]any{{
			"header": "Name", "question": "What should it be called?", "custom": true,
		}},
	}}}
	server := fake.start(t)
	src := NewOpenCodeSource(server.URL)
	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Question == nil || !sessions[0].Question.Custom {
		t.Fatalf("custom-only question was hidden: %+v", sessions)
	}
	if err := src.Answer(context.Background(), "opencode:ses_1", currentOpenCodeAnswer(
		t, src, "opencode:ses_1", protocol.QuestionAnswer{Text: "Falcon"},
	)); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCodeQuestionsAreAnsweredSequentially(t *testing.T) {
	fake := &ocFake{questions: []map[string]any{{
		"id": "que_1", "sessionID": "ses_1",
		"questions": []map[string]any{
			{
				"header": "Database", "question": "Which database?",
				"options": []map[string]any{{"label": "Postgres"}, {"label": "SQLite"}},
			},
			{
				"header": "Region", "question": "Which region?",
				"options": []map[string]any{{"label": "Dublin"}, {"label": "Virginia"}},
			},
		},
	}}}
	server := fake.start(t)
	src := NewOpenCodeSource(server.URL)
	ctx := context.Background()

	sessions, err := src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Question == nil {
		t.Fatalf("first pending question was not discovered: %+v", sessions)
	}
	if got := sessions[0].Question.Prompt; got != "Which database?" {
		t.Fatalf("first prompt = %q", got)
	}
	if got := sessions[0].Question.Detail; got != "Question 1 of 2" {
		t.Fatalf("first detail = %q", got)
	}
	firstAnswer := currentOpenCodeAnswer(
		t, src, "opencode:ses_1", protocol.QuestionAnswer{OptionKey: "Postgres"},
	)
	if err := src.Answer(ctx, "opencode:ses_1", firstAnswer); err != nil {
		t.Fatal(err)
	}
	if err := src.Answer(ctx, "opencode:ses_1", firstAnswer); err == nil {
		t.Fatal("a second answer to question one overwrote the first before discovery")
	}
	if got := fake.requestCount(); got != 0 {
		t.Fatalf("sent %d replies after only the first answer, want 0", got)
	}

	sessions, err = src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Question == nil {
		t.Fatalf("second pending question was not discovered: %+v", sessions)
	}
	if got := sessions[0].Question.Prompt; got != "Which region?" {
		t.Fatalf("second prompt = %q", got)
	}
	if got := sessions[0].Question.Detail; got != "Question 2 of 2" {
		t.Fatalf("second detail = %q", got)
	}
	if err := src.Answer(ctx, "opencode:ses_1", currentOpenCodeAnswer(
		t, src, "opencode:ses_1", protocol.QuestionAnswer{OptionKey: "Dublin"},
	)); err != nil {
		t.Fatal(err)
	}
	request := fake.lastRequest()
	if request.path != "/question/que_1/reply" {
		t.Fatalf("reply path = %q", request.path)
	}
	answers, ok := request.body["answers"].([]any)
	if !ok || len(answers) != 2 {
		t.Fatalf("reply body = %#v", request.body)
	}
	first, firstOK := answers[0].([]any)
	second, secondOK := answers[1].([]any)
	if !firstOK || len(first) != 1 || first[0] != "Postgres" ||
		!secondOK || len(second) != 1 || second[0] != "Dublin" {
		t.Fatalf("answers = %#v", answers)
	}
}

func TestOpenCodePermissionCanBeAnswered(t *testing.T) {
	fake := &ocFake{permissions: []map[string]any{{
		"id": "per_1", "sessionID": "ses_1", "permission": "bash",
		"patterns": []string{"go test ./..."}, "always": []string{"go test *"},
		"metadata": map[string]any{"command": "go test ./...", "cwd": "/Users/me/code"},
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
	detail := sessions[0].Question.Detail
	for _, want := range []string{
		"Requested scope:\ngo test ./...",
		`"command": "go test ./..."`,
		`"cwd": "/Users/me/code"`,
		"Persistent scope:\ngo test *",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("permission detail %q does not contain %q", detail, want)
		}
	}
	if got := sessions[0].Question.Options[1].Description; !strings.Contains(got, "go test *") {
		t.Errorf("persistent approval description = %q", got)
	}
	if err := src.Answer(ctx, "opencode:ses_1", currentOpenCodeAnswer(
		t, src, "opencode:ses_1", protocol.QuestionAnswer{OptionKey: "once"},
	)); err != nil {
		t.Fatal(err)
	}
	request := fake.lastRequest()
	if request.path != "/permission/per_1/reply" || request.body["reply"] != "once" {
		t.Fatalf("permission reply = %#v", request)
	}
}

func currentOpenCodeAnswer(
	t *testing.T,
	src *OpenCodeSource,
	sessionID string,
	answer protocol.QuestionAnswer,
) protocol.QuestionAnswer {
	t.Helper()
	src.mu.RLock()
	session, ok := src.sessions[sessionID]
	src.mu.RUnlock()
	if !ok || session.meta.Question == nil || session.meta.Question.ID == "" {
		t.Fatalf("session %q has no current question revision", sessionID)
	}
	answer.QuestionID = session.meta.Question.ID
	return answer
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

func TestOpenCodePartialServerFailurePreservesItsSessions(t *testing.T) {
	first := &ocFake{sessionID: "ses_1", directory: "/Users/me/one"}
	second := &ocFake{sessionID: "ses_2", directory: "/Users/me/two"}
	firstServer := first.start(t)
	secondServer := second.start(t)

	src := NewOpenCodeSource(firstServer.URL)
	src.findServers = func(context.Context) []string {
		return []string{firstServer.URL, secondServer.URL}
	}
	if sessions, err := src.Discover(context.Background()); err != nil || len(sessions) != 2 {
		t.Fatalf("initial discovery = %d sessions, %v", len(sessions), err)
	}

	second.setSessionFailure(true)
	sessions, err := src.Discover(context.Background())
	if err == nil {
		t.Fatal("partial API failure was not reported")
	}
	if len(sessions) != 2 {
		t.Fatalf("partial discovery dropped the failed server's cached session: %+v", sessions)
	}
	if _, err := src.Page(context.Background(), "opencode:ses_2", "", 20); err != nil {
		t.Fatalf("cached route was discarded after a partial failure: %v", err)
	}
}

func TestOpenCodeRepeatedSnapshotFailureExpiresServer(t *testing.T) {
	working := &ocFake{sessionID: "ses_1", directory: "/Users/me/one"}
	server := working.start(t)
	src := NewOpenCodeSource(server.URL)
	src.findServers = func(context.Context) []string { return []string{server.URL} }
	if sessions, err := src.Discover(context.Background()); err != nil || len(sessions) != 1 {
		t.Fatalf("initial discovery = %d sessions, %v", len(sessions), err)
	}

	working.setSessionFailure(true)
	for miss := 1; miss <= openCodeHealthMissGrace; miss++ {
		sessions, err := src.Discover(context.Background())
		if err == nil || len(sessions) != 1 {
			t.Fatalf("snapshot failure %d = %d sessions, %v", miss, len(sessions), err)
		}
	}
	sessions, err := src.Discover(context.Background())
	if err == nil || sessions == nil || len(sessions) != 0 {
		t.Fatalf("sustained snapshot failure did not expire route: %#v, %v", sessions, err)
	}
}

func TestOpenCodeTransientHealthMissPreservesSessions(t *testing.T) {
	fake := &ocFake{sessionID: "ses_1", directory: "/Users/me/one"}
	server := fake.start(t)
	src := NewOpenCodeSource(server.URL)
	available := true
	src.findServers = func(context.Context) []string {
		if available {
			return []string{server.URL}
		}
		return nil
	}

	if sessions, err := src.Discover(context.Background()); err != nil || len(sessions) != 1 {
		t.Fatalf("initial discover = %+v, %v", sessions, err)
	}
	available = false
	for miss := 1; miss <= openCodeHealthMissGrace; miss++ {
		sessions, err := src.Discover(context.Background())
		if err == nil || len(sessions) != 1 {
			t.Fatalf("health miss %d dropped sessions: %+v, %v", miss, sessions, err)
		}
	}
	if sessions, err := src.Discover(context.Background()); err != nil || len(sessions) != 0 {
		t.Fatalf("sustained health misses did not remove sessions: %+v, %v", sessions, err)
	}
}

func TestOpenCodeHealthGraceIsPerServer(t *testing.T) {
	first := &ocFake{sessionID: "ses_1", directory: "/Users/me/one"}
	second := &ocFake{sessionID: "ses_2", directory: "/Users/me/two"}
	firstServer := first.start(t)
	secondServer := second.start(t)

	src := NewOpenCodeSource(firstServer.URL)
	servers := []string{firstServer.URL, secondServer.URL}
	src.findServers = func(context.Context) []string { return servers }
	if sessions, err := src.Discover(context.Background()); err != nil || len(sessions) != 2 {
		t.Fatalf("initial discovery = %d sessions, %v", len(sessions), err)
	}

	// One process remains healthy throughout. Its presence must not reset the
	// independent grace counter for the missing second process.
	servers = []string{firstServer.URL}
	for miss := 1; miss <= openCodeHealthMissGrace; miss++ {
		sessions, err := src.Discover(context.Background())
		if err == nil || len(sessions) != 2 {
			t.Fatalf("partial health miss %d = %d sessions, %v", miss, len(sessions), err)
		}
	}
	sessions, err := src.Discover(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].ID != "opencode:ses_1" {
		t.Fatalf("expired server remained or healthy server vanished: %+v, %v", sessions, err)
	}
}

func TestOpenCodeHealthResponseIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxOpenCodeHealthResponse+1)))
	}))
	defer server.Close()

	src := NewOpenCodeSource(server.URL)
	if src.Available(context.Background()) {
		t.Fatal("an oversized health response was accepted")
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

func TestOpenCodeDoesNotBroadcastPasswordDuringDiscovery(t *testing.T) {
	t.Setenv("OPENCODE_SERVER_PASSWORD", "secret")
	src := NewOpenCodeSource("")
	if err := src.Validate(); err == nil || !strings.Contains(err.Error(), "AGENTMAN_OPENCODE_URL") {
		t.Fatalf("missing pinned URL did not produce actionable error: %v", err)
	}
	var requests atomic.Int32
	src.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, fmt.Errorf("unexpected discovery request")
	})

	if servers := src.scanServers(context.Background()); len(servers) != 0 {
		t.Fatalf("authenticated unpinned discovery returned servers: %v", servers)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("password discovery made %d requests; expected none", got)
	}
}

func TestOpenCodeConfigurationRequiresTLSOffLoopback(t *testing.T) {
	t.Setenv("OPENCODE_SERVER_PASSWORD", "secret")
	for _, raw := range []string{
		"http://example.com:4096",
		"ws://127.0.0.1:4096",
		"https://user:pass@example.com",
		"https://example.com/api",
	} {
		if err := NewOpenCodeSource(raw).Validate(); err == nil {
			t.Errorf("unsafe OpenCode URL %q was accepted", raw)
		}
	}
	for _, raw := range []string{
		"http://127.0.0.1:4096",
		"http://[::1]:4096",
		"http://localhost:4096",
		"https://example.com:4096",
	} {
		if err := NewOpenCodeSource(raw).Validate(); err != nil {
			t.Errorf("safe OpenCode URL %q was rejected: %v", raw, err)
		}
	}
}

func TestOpenCodeSnapshotRejectsExcessSessionsBeforeFanout(t *testing.T) {
	var scopedRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session":
			items := make([]map[string]any, maxOpenCodeSessions+1)
			for index := range items {
				items[index] = map[string]any{
					"id": fmt.Sprintf("ses_%d", index), "directory": "/tmp/project",
					"time": map[string]any{"updated": time.Now().UnixMilli()},
				}
			}
			_ = json.NewEncoder(w).Encode(items)
		default:
			scopedRequests.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}
	}))
	defer server.Close()

	src := NewOpenCodeSource(server.URL)
	_, _, _, _, err := src.snapshotAt(context.Background(), server.URL, 0)
	if err == nil || !strings.Contains(err.Error(), "maximum is 200") {
		t.Fatalf("snapshot error = %v, want session-count rejection", err)
	}
	if got := scopedRequests.Load(); got != 0 {
		t.Fatalf("started %d scoped requests after rejecting the session list", got)
	}
}

func TestOpenCodeSnapshotBoundsRequestConcurrency(t *testing.T) {
	const directoryCount = 20
	var active atomic.Int32
	var peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session" {
			items := make([]map[string]any, directoryCount)
			for index := range items {
				items[index] = map[string]any{
					"id":        fmt.Sprintf("ses_%d", index),
					"directory": fmt.Sprintf("/tmp/project-%d", index),
					"time":      map[string]any{"updated": time.Now().UnixMilli()},
				}
			}
			_ = json.NewEncoder(w).Encode(items)
			return
		}

		current := active.Add(1)
		for {
			previous := peak.Load()
			if current <= previous || peak.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		active.Add(-1)
		if r.URL.Path == "/session/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{})
		} else {
			_ = json.NewEncoder(w).Encode([]any{})
		}
	}))
	defer server.Close()

	src := NewOpenCodeSource(server.URL)
	if _, _, _, _, err := src.snapshotAt(context.Background(), server.URL, 0); err != nil {
		t.Fatal(err)
	}
	if got := peak.Load(); got > maxOpenCodeSnapshotWorkers {
		t.Fatalf("peak request concurrency = %d, maximum is %d", got, maxOpenCodeSnapshotWorkers)
	} else if got < 2 {
		t.Fatalf("snapshot ran serially; peak concurrency = %d", got)
	}
}

func TestOpenCodeDiscoveryDoesNotFetchEveryMissingModel(t *testing.T) {
	var messageRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/global/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"healthy": true})
		case r.URL.Path == "/session":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id": "ses_1", "title": "model probe", "directory": "/tmp/project",
				"time": map[string]any{"updated": time.Now().UnixMilli()},
			}})
		case r.URL.Path == "/session/status":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case r.URL.Path == "/question" || r.URL.Path == "/permission":
			_ = json.NewEncoder(w).Encode([]any{})
		case strings.HasSuffix(r.URL.Path, "/message"):
			messageRequests.Add(1)
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"info": map[string]any{
					"id": "msg_1", "role": "assistant", "modelID": "fast-model",
					"time": map[string]any{"created": time.Now().UnixMilli()},
				},
				"parts": []map[string]any{{"id": "text-0", "type": "text", "text": "ready"}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	src := NewOpenCodeSource(server.URL)
	sessions, err := src.Discover(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("discover = %+v, %v", sessions, err)
	}
	if sessions[0].Model != "" || messageRequests.Load() != 0 {
		t.Fatalf("discovery fetched a cosmetic model: session=%+v requests=%d", sessions[0], messageRequests.Load())
	}
	if _, err := src.Page(context.Background(), sessions[0].ID, "", 20); err != nil {
		t.Fatal(err)
	}
	sessions, err = src.Discover(context.Background())
	if err != nil || sessions[0].Model != "fast-model" {
		t.Fatalf("model was not cached from the normal page read: %+v, %v", sessions, err)
	}
	if got := messageRequests.Load(); got != 1 {
		t.Fatalf("message requests = %d, want only the explicit page read", got)
	}
}
