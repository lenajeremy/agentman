package hook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func cursorHooksPath(home string) string { return filepath.Join(home, ".cursor", "hooks.json") }

func cursorPlan(t *testing.T, home, binary string, remove bool) Plan {
	t.Helper()
	plans, err := Installer{Home: home, Binary: binary}.Plans("tok", remove)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range plans {
		if plan.Kind == protocol.KindCursorCLI {
			return plan
		}
	}
	t.Fatal("no Cursor plan")
	return Plan{}
}

func cursorCommands(t *testing.T, home string) map[string][]string {
	t.Helper()
	var config struct {
		Version int `json:"version"`
		Hooks   map[string][]struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	raw, err := os.ReadFile(cursorHooksPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if config.Version != 1 {
		t.Errorf("version = %d, want 1", config.Version)
	}
	out := map[string][]string{}
	for event, entries := range config.Hooks {
		for _, entry := range entries {
			out[event] = append(out[event], entry.Command)
		}
	}
	return out
}

func TestCursorHooksInstallIntoAFreshHome(t *testing.T) {
	home := t.TempDir()
	plan := cursorPlan(t, home, "/Applications/Agent Man/am", false)
	if !plan.Changed || plan.Err != nil {
		t.Fatalf("plan = %+v", plan)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	got := cursorCommands(t, home)
	want := map[string]string{
		"sessionStart":       "'/Applications/Agent Man/am' hook cursor-cli SessionStart",
		"beforeSubmitPrompt": "'/Applications/Agent Man/am' hook cursor-cli UserPromptSubmit",
		"stop":               "'/Applications/Agent Man/am' hook cursor-cli Stop",
		"sessionEnd":         "'/Applications/Agent Man/am' hook cursor-cli SessionEnd",
	}
	for event, command := range want {
		if len(got[event]) != 1 || got[event][0] != command {
			t.Errorf("%s = %q, want %q", event, got[event], command)
		}
	}
	if len(got) != len(want) {
		t.Errorf("events = %v; nothing else is installed", got)
	}
	if info, err := os.Stat(cursorHooksPath(home)); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("hooks.json mode: %v %v", info, err)
	}
}

// The file is the user's: their own hooks and settings survive, a second
// install changes nothing, and uninstall takes only ours.
func TestCursorHooksKeepTheUsersOwnAndAreIdempotent(t *testing.T) {
	home := t.TempDir()
	user := `{
  "version": 1,
  "hooks": {
    "stop": [{"command": "./scripts/notify.sh", "loop_limit": 3}],
    "afterFileEdit": [{"command": "prettier --write"}]
  },
  "teamPolicy": "keep me"
}`
	if err := os.MkdirAll(filepath.Dir(cursorHooksPath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cursorHooksPath(home), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cursorPlan(t, home, "/usr/local/bin/am", false).Apply(); err != nil {
		t.Fatal(err)
	}
	got := cursorCommands(t, home)
	if len(got["stop"]) != 2 || got["stop"][0] != "./scripts/notify.sh" || got["afterFileEdit"][0] != "prettier --write" {
		t.Fatalf("user hooks not kept: %v", got)
	}
	raw, _ := os.ReadFile(cursorHooksPath(home))
	if !strings.Contains(string(raw), `"teamPolicy": "keep me"`) || !strings.Contains(string(raw), `"loop_limit": 3`) {
		t.Fatalf("unknown keys lost:\n%s", raw)
	}
	if backup, err := os.ReadFile(cursorHooksPath(home) + ".agentman.bak"); err != nil || string(backup) != user {
		t.Errorf("backup = %q, %v", backup, err)
	}

	// A reinstall from a moved binary replaces ours rather than adding more.
	again := cursorPlan(t, home, "/opt/homebrew/bin/am", false)
	if err := again.Apply(); err != nil {
		t.Fatal(err)
	}
	if got := cursorCommands(t, home); len(got["stop"]) != 2 || got["stop"][1] != "/opt/homebrew/bin/am hook cursor-cli Stop" {
		t.Fatalf("reinstall = %v", got["stop"])
	}
	if cursorPlan(t, home, "/opt/homebrew/bin/am", false).Changed {
		t.Error("a second identical install reports changes")
	}

	if err := cursorPlan(t, home, "/opt/homebrew/bin/am", true).Apply(); err != nil {
		t.Fatal(err)
	}
	got = cursorCommands(t, home)
	if len(got["stop"]) != 1 || got["stop"][0] != "./scripts/notify.sh" || got["sessionStart"] != nil {
		t.Fatalf("uninstall left %v", got)
	}
}

// Someone else's program that happens to take "hook cursor-cli" is not ours.
func TestCursorHooksLeaveSimilarCommandsAlone(t *testing.T) {
	for command, ours := range map[string]bool{
		"/usr/local/bin/am hook cursor-cli Stop":        true,
		"'/Users/x/My Apps/am' hook cursor-cli Stop":    true,
		"/usr/local/bin/agentman hook cursor-cli Stop":  true,
		"/usr/local/bin/spam hook cursor-cli Stop":      false,
		"hook cursor-cli Stop":                          false,
		"/usr/local/bin/am hook claude Stop":            false,
		"bash -c 'am hook cursor-cli Stop; echo hello'": false,
	} {
		if got := isOurCursorHook(map[string]any{"command": command}); got != ours {
			t.Errorf("%q: ours=%v, want %v", command, got, ours)
		}
	}
}

func TestCursorHooksRefuseAFileTheyCannotRead(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(cursorHooksPath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	commented := "{\n  // my hooks\n  \"version\": 1, \"hooks\": {}\n}"
	if err := os.WriteFile(cursorHooksPath(home), []byte(commented), 0o600); err != nil {
		t.Fatal(err)
	}
	if plan := cursorPlan(t, home, "/usr/local/bin/am", false); plan.Err == nil {
		t.Fatal("a file with comments would have been rewritten without them")
	}
	empty := t.TempDir()
	if plan := cursorPlan(t, empty, "/usr/local/bin/am", true); plan.Changed || plan.Err != nil {
		t.Fatalf("uninstall with nothing installed = %+v", plan)
	}
	if _, err := os.Stat(cursorHooksPath(empty)); !os.IsNotExist(err) {
		t.Fatal("uninstall created a hooks file")
	}
}

// A real Cursor stop payload (user_email redacted at the source; it is never
// read): the chat id is the session, and how the turn ended is kept.
func TestCursorStopPayloadIsDelivered(t *testing.T) {
	server := NewServer("tok")
	body := `{"conversation_id":"305d21db-5149-4687-98f4-a2c7b137b809","generation_id":"g","model":"default",
		"status":"error","loop_count":0,"input_tokens":16119,"session_id":"305d21db-5149-4687-98f4-a2c7b137b809",
		"hook_event_name":"stop","cursor_version":"2026.09.26-dd393fe","workspace_roots":["/w"],
		"user_email":"someone@example.com","transcript_path":"/Users/u/.cursor/projects/w/agent-transcripts/305d/305d.jsonl"}`
	request := httptest.NewRequest(http.MethodPost, "/hook/cursor-cli/Stop", strings.NewReader(body))
	request.Header.Set("X-Agentman-Token", "tok")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	event := <-server.Events()
	if event.SessionID != "cursor-cli:305d21db-5149-4687-98f4-a2c7b137b809" || event.Payload.Status != "error" ||
		!event.IsTurnComplete() {
		t.Fatalf("event = %+v", event)
	}
	if encoded, _ := json.Marshal(event); strings.Contains(string(encoded), "someone@example.com") {
		t.Fatal("the user's email was kept in the event")
	}

	request = httptest.NewRequest(http.MethodPost, "/hook/cursor-cli/Notification", strings.NewReader(body))
	request.Header.Set("X-Agentman-Token", "tok")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("an event Cursor never sends was accepted: %d", response.Code)
	}
}

// A stop the user caused is not news.
func TestCursorAbortedTurnDoesNotRing(t *testing.T) {
	event := Event{Kind: protocol.KindCursorCLI, Name: NameStop, Payload: Payload{Status: "aborted"}}
	if event.IsTurnComplete() {
		t.Fatal("an aborted turn was announced as finished")
	}
}

// The queued-message reply is Claude's shape, which Cursor's stop hook turns
// into a follow-up message.
func TestCursorStopHookAnswersWithTheQueuedMessage(t *testing.T) {
	server := NewServer("tok")
	server.SetPendingSource(func(id string) []string {
		if id == "cursor-cli:chat-1" {
			return []string{"run the tests next"}
		}
		return nil
	})
	request := httptest.NewRequest(http.MethodPost, "/hook/cursor-cli/Stop",
		strings.NewReader(`{"session_id":"chat-1","hook_event_name":"stop","status":"completed"}`))
	request.Header.Set("X-Agentman-Token", "tok")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var decision struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decision); err != nil || decision.Decision != "block" ||
		decision.Reason != "run the tests next" {
		t.Fatalf("reply = %s", response.Body.String())
	}
}
