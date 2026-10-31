package source

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// ocServiceFake stands in for OpenCode 2.2's background service: its API
// under /api, behind a password, in OpenCode 2's shapes.
type ocServiceFake struct {
	mu          sync.Mutex
	messages    []map[string]any // newest first, as OpenCode 2 pages
	forms       []map[string]any
	permissions []map[string]any
	active      bool
	posts       []ocServicePost
}

type ocServicePost struct {
	path string
	body map[string]any
}

func (f *ocServiceFake) start(t *testing.T) *httptest.Server {
	t.Helper()
	now := float64(time.Now().UnixMilli())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, password, ok := r.BasicAuth(); !ok || user != "opencode" || password != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"_tag":"UnauthorizedError","message":"Authentication required"}`)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		reply := func(value any) { _ = json.NewEncoder(w).Encode(value) }
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.posts = append(f.posts, ocServicePost{path: r.URL.Path, body: body})
			reply(map[string]any{})
			return
		}
		switch r.URL.Path {
		case "/api/info":
			reply(map[string]any{"version": "2.2.0", "pid": 1, "urls": []string{}, "paths": map[string]string{"tmp": "/tmp"}})
		case "/api/session":
			reply(map[string]any{"data": []map[string]any{
				{"id": "ses_1", "projectID": "p", "title": "Fix the build", "location": map[string]string{"directory": "/w/app"},
					"model": map[string]string{"id": "gpt-6", "providerID": "openai"},
					"time":  map[string]float64{"created": now - 60000, "updated": now}},
				{"id": "ses_child", "parentID": "ses_1", "projectID": "p", "title": "A subagent",
					"location": map[string]string{"directory": "/w/app"}, "time": map[string]float64{"created": now, "updated": now}},
			}, "cursor": map[string]any{}})
		case "/api/session/active":
			active := map[string]any{}
			if f.active {
				active["ses_1"] = map[string]string{"type": "running"}
			}
			reply(map[string]any{"data": active})
		case "/api/form":
			reply(map[string]any{"location": map[string]string{"directory": r.URL.Query().Get("location[directory]")}, "data": f.forms})
		case "/api/permission/request":
			reply(map[string]any{"location": map[string]string{"directory": r.URL.Query().Get("location[directory]")}, "data": f.permissions})
		case "/api/session/ses_1/message":
			if r.URL.Query().Get("cursor") == "older" {
				reply(map[string]any{"data": []any{}, "cursor": map[string]any{"next": nil}})
				return
			}
			reply(map[string]any{"data": f.messages, "cursor": map[string]any{"next": "older"}})
		case "/api/skill":
			if r.URL.Query().Get("location[directory]") != "/w/app" {
				reply(map[string]any{"data": []any{}})
				return
			}
			reply(map[string]any{"location": map[string]string{"directory": "/w/app"}, "data": []map[string]string{
				{"id": "frontend-design", "name": "Frontend Design", "content": "# Frontend Design"},
			}})
		case "/api/model":
			reply(map[string]any{"data": []map[string]string{
				{"id": "gpt-6", "modelID": "gpt-6", "providerID": "openai"},
				{"id": "big-pickle", "modelID": "big-pickle", "providerID": "opencode"},
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func (f *ocServiceFake) lastPost(t *testing.T) ocServicePost {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.posts) == 0 {
		t.Fatal("nothing was sent")
	}
	return f.posts[len(f.posts)-1]
}

// openCodeServiceSource is an adapter that finds the fake through a
// service.json, as it finds OpenCode 2's own.
func openCodeServiceSource(t *testing.T, url, password string) *OpenCodeSource {
	t.Helper()
	file := filepath.Join(t.TempDir(), "service.json")
	raw, _ := json.Marshal(map[string]any{"id": "x", "version": "2.2.0", "url": url, "pid": 1, "password": password})
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	src := NewOpenCodeSource("")
	src.serviceFile = file
	src.findServers = src.findServices
	return src
}

// The skills a message names go beside it, by id, when the service has
// them for the folder; one it doesn't know is left to the words, since it
// would fail the whole message.
func TestOpenCodeServiceTakesTheSkillsAMessageNames(t *testing.T) {
	fake := &ocServiceFake{}
	server := fake.start(t)
	src := openCodeServiceSource(t, server.URL, "secret")
	ctx := context.Background()
	if _, err := src.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	text := "Polish it with the frontend-design skill and the nope skill."
	if mode, err := src.InjectWithSkills(ctx, "opencode:ses_1", text, []string{"frontend-design", "nope"}); err != nil || mode != protocol.InjectAPI {
		t.Fatalf("inject: %v %v", mode, err)
	}
	post := fake.lastPost(t)
	if post.path != "/api/session/ses_1/prompt" || post.body["text"] != text {
		t.Fatalf("sent %+v", post)
	}
	skills, _ := post.body["skills"].([]any)
	if len(skills) != 1 {
		t.Fatalf("skills = %v", post.body["skills"])
	}
	if first, _ := skills[0].(map[string]any); first["id"] != "frontend-design" || first["name"] != "Frontend Design" {
		t.Errorf("skill = %v", first)
	}

	// None it knows: the words alone.
	if _, err := src.InjectWithSkills(ctx, "opencode:ses_1", "use the nope skill", []string{"nope"}); err != nil {
		t.Fatal(err)
	}
	if post := fake.lastPost(t); post.body["skills"] != nil {
		t.Errorf("sent an unknown skill: %v", post.body)
	}
}

func TestOpenCodeServiceSessionsAndMessages(t *testing.T) {
	fake := &ocServiceFake{active: true, messages: []map[string]any{
		{"id": "msg_a", "type": "assistant", "time": map[string]float64{"created": 3000, "completed": 4000},
			"agent": "build", "model": map[string]string{"id": "gpt-6", "providerID": "openai"},
			"content": []map[string]any{
				{"type": "reasoning", "text": "thinking"},
				{"type": "tool", "id": "call_1", "name": "shell",
					"state": map[string]any{"status": "completed", "input": map[string]string{"command": "go build ./..."},
						"content": []map[string]string{{"type": "text", "text": "ok"}}}},
				{"type": "text", "text": "The build passes."},
			}},
		{"id": "msg_s", "type": "system", "time": map[string]float64{"created": 2500}, "text": "tools changed"},
		{"id": "msg_u", "type": "user", "time": map[string]float64{"created": 2000}, "text": "fix the build"},
	}}
	server := fake.start(t)
	src := openCodeServiceSource(t, server.URL, "secret")
	ctx := context.Background()

	sessions, err := src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "opencode:ses_1" || sessions[0].Cwd != "/w/app" ||
		sessions[0].State != protocol.StateBusy {
		t.Fatalf("sessions = %+v", sessions)
	}

	page, err := src.Page(ctx, "opencode:ses_1", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, message := range page.Messages {
		entry := string(message.Role) + ":" + message.Text
		if message.Tool != nil {
			entry = "tool:" + message.Tool.Name + " " + message.Tool.Summary + " → " + message.Text
		}
		got = append(got, entry)
	}
	want := []string{"user:fix the build", "tool:shell go build ./... → ok", "assistant:The build passes."}
	if !slices.Equal(got, want) {
		t.Fatalf("page = %q, want %q", got, want)
	}
	if !page.HasMore || page.NextCursor != "older" {
		t.Fatalf("cursor = %q (more %v)", page.NextCursor, page.HasMore)
	}
	older, err := src.Page(ctx, "opencode:ses_1", page.NextCursor, 20)
	if err != nil || len(older.Messages) != 0 || older.HasMore {
		t.Fatalf("older page = %+v, %v", older, err)
	}
}

// The password goes only to the address beside it, and only when that is
// this Mac's own; a wrong one finds nothing.
func TestOpenCodeServiceNeedsItsOwnPasswordAndAddress(t *testing.T) {
	fake := &ocServiceFake{}
	server := fake.start(t)
	if sessions, err := openCodeServiceSource(t, server.URL, "wrong").Discover(context.Background()); err != nil || len(sessions) != 0 {
		t.Fatalf("with the wrong password: %+v, %v", sessions, err)
	}
	remote := strings.Replace(server.URL, "127.0.0.1", "example.com", 1)
	if _, ok := readOpenCodeService(func() string {
		file := filepath.Join(t.TempDir(), "service.json")
		raw, _ := json.Marshal(map[string]string{"url": remote, "password": "secret"})
		_ = os.WriteFile(file, raw, 0o600)
		return file
	}()); ok {
		t.Fatal("took a service that is not on this Mac")
	}
}

func TestOpenCodeServiceTakesMessagesAndInterrupts(t *testing.T) {
	fake := &ocServiceFake{}
	server := fake.start(t)
	src := openCodeServiceSource(t, server.URL, "secret")
	ctx := context.Background()
	if _, err := src.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	if mode, err := src.Inject(ctx, "opencode:ses_1", "run the tests"); err != nil || mode != protocol.InjectAPI {
		t.Fatalf("inject: %v %v", mode, err)
	}
	if post := fake.lastPost(t); post.path != "/api/session/ses_1/prompt" || post.body["text"] != "run the tests" {
		t.Fatalf("sent %+v", post)
	}
	if err := src.Interrupt(ctx, "opencode:ses_1"); err != nil {
		t.Fatal(err)
	}
	if post := fake.lastPost(t); post.path != "/api/session/ses_1/interrupt" {
		t.Fatalf("sent %+v", post)
	}
	// The models offered come from OpenCode 2's list.
	catalog, err := src.catalogFor(ctx, server.URL)
	if err != nil || !slices.Equal(catalog.names, []string{"gpt-6", "big-pickle"}) && !slices.Equal(catalog.names, []string{"big-pickle", "gpt-6"}) {
		t.Fatalf("models = %+v, %v", catalog, err)
	}
}

func TestOpenCodeServicePermissionIsAnswered(t *testing.T) {
	fake := &ocServiceFake{permissions: []map[string]any{{
		"id": "per_1", "sessionID": "ses_1", "action": "shell", "resources": []string{"rm -rf build"}, "save": []string{"rm *"},
	}}}
	server := fake.start(t)
	src := openCodeServiceSource(t, server.URL, "secret")
	ctx := context.Background()
	sessions, err := src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Question == nil || sessions[0].Question.Prompt != "Allow shell?" ||
		!strings.Contains(sessions[0].Question.Detail, "rm -rf build") || len(sessions[0].Question.Options) != 3 {
		t.Fatalf("question = %+v", sessions)
	}
	if err := src.Answer(ctx, "opencode:ses_1", currentOpenCodeAnswer(t, src, "opencode:ses_1",
		protocol.QuestionAnswer{OptionKey: "once"})); err != nil {
		t.Fatal(err)
	}
	if post := fake.lastPost(t); post.path != "/api/session/ses_1/permission/per_1/reply" || post.body["decision"] != "once" {
		t.Fatalf("sent %+v", post)
	}
}

// OpenCode 2 asks with a form: each field a question on the phone, one at a
// time, answered by its key with the options' values.
func TestOpenCodeServiceFormIsAnsweredFieldByField(t *testing.T) {
	fake := &ocServiceFake{forms: []map[string]any{{
		"id": "frm_1", "sessionID": "ses_1", "title": "Setup",
		"fields": []map[string]any{
			{"key": "db", "type": "string", "title": "Which database?",
				"options": []map[string]string{{"value": "pg", "label": "Postgres"}, {"value": "lite", "label": "SQLite"}}},
			{"key": "token", "type": "string", "title": "Secret", "hidden": true},
			{"key": "extras", "type": "multiselect", "title": "Extras?",
				"options": []map[string]string{{"value": "auth", "label": "Auth"}, {"value": "ci", "label": "CI"}}},
		},
	}}}
	server := fake.start(t)
	src := openCodeServiceSource(t, server.URL, "secret")
	ctx := context.Background()

	sessions, err := src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Question == nil || sessions[0].Question.Prompt != "Which database?" ||
		sessions[0].Question.Title != "Setup" || sessions[0].Question.Detail != "Question 1 of 2" || sessions[0].Question.Custom {
		t.Fatalf("first question = %+v", sessions[0].Question)
	}
	if err := src.Answer(ctx, "opencode:ses_1", currentOpenCodeAnswer(t, src, "opencode:ses_1",
		protocol.QuestionAnswer{OptionKey: "Postgres"})); err != nil {
		t.Fatal(err)
	}
	if sessions, err = src.Discover(ctx); err != nil || sessions[0].Question.Prompt != "Extras?" || !sessions[0].Question.Multiple {
		t.Fatalf("second question = %+v, %v", sessions[0].Question, err)
	}
	if err := src.Answer(ctx, "opencode:ses_1", currentOpenCodeAnswer(t, src, "opencode:ses_1",
		protocol.QuestionAnswer{Options: []string{"Auth", "CI"}})); err != nil {
		t.Fatal(err)
	}
	post := fake.lastPost(t)
	answer, _ := post.body["answer"].(map[string]any)
	extras, _ := answer["extras"].([]any)
	if post.path != "/api/session/ses_1/form/frm_1/reply" || answer["db"] != "pg" || len(extras) != 2 ||
		extras[0] != "auth" || extras[1] != "ci" || answer["token"] != nil {
		t.Fatalf("sent %+v", post)
	}
}

func TestOpenCodeMajorVersion(t *testing.T) {
	for output, want := range map[string]int{"opencode v2.2.0": 2, "1.4.3": 1, "": 0} {
		dir := t.TempDir()
		script := "#!/bin/sh\necho '" + output + "'\n"
		if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir)
		if got := OpenCodeMajorVersion(context.Background()); got != want {
			t.Errorf("%q: %d, want %d", output, got, want)
		}
	}
	t.Setenv("PATH", t.TempDir())
	if got := OpenCodeMajorVersion(context.Background()); got != 0 {
		t.Errorf("without opencode: %d", got)
	}
}
