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
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

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
	if err := src.Answer(ctx, "opencode:ses_1", protocol.QuestionAnswer{OptionKey: "Postgres"}); err != nil {
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
	if err := src.Answer(ctx, "opencode:ses_1", protocol.QuestionAnswer{OptionKey: "Postgres"}); err != nil {
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
	answer := protocol.QuestionAnswer{Options: []string{"API", "CLI"}, Text: "Desktop"}
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
	if err := src.Answer(ctx, "opencode:ses_1", protocol.QuestionAnswer{OptionKey: "Focused"}); err != nil {
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
	if err := src.Answer(ctx, "opencode:ses_1", protocol.QuestionAnswer{Text: "Agentman"}); err != nil {
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
		if err := src.Answer(ctx, sessionID, step.answer); err != nil {
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
	if err := src.Answer(context.Background(), "opencode:ses_1", protocol.QuestionAnswer{Text: "Falcon"}); err != nil {
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
	if err := src.Answer(ctx, "opencode:ses_1", protocol.QuestionAnswer{OptionKey: "Postgres"}); err != nil {
		t.Fatal(err)
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
	if err := src.Answer(ctx, "opencode:ses_1", protocol.QuestionAnswer{OptionKey: "Dublin"}); err != nil {
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
	if err := src.Answer(ctx, "opencode:ses_1", protocol.QuestionAnswer{OptionKey: "once"}); err != nil {
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
