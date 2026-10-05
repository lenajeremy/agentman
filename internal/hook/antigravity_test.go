package hook

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

const agyConversation = "ef116096-84c3-4723-bbc3-aed0f3cca68b"

// agyStop is the Stop payload agy 1.2.17 wrote, with the home folder renamed.
func agyStop(home, conversation, app string) string {
	brain := filepath.Join(home, ".gemini", app, "brain", conversation)
	return `{"artifactDirectoryPath":"` + brain + `","conversationId":"` + conversation +
		`","error":"","executionNum":0,"fullyIdle":true,"modelName":"gemini-3.8-flash-high",` +
		`"terminationReason":"NO_TOOL_CALL","transcriptPath":"` + brain +
		`/.system_generated/logs/transcript_full.jsonl","workspacePaths":["/Users/me/work/api"]}`
}

func TestParseAntigravityPayload(t *testing.T) {
	home := t.TempDir()
	p, err := ParsePayload([]byte(agyStop(home, agyConversation, "antigravity-cli")))
	if err != nil {
		t.Fatal(err)
	}
	if p.SessionID != agyConversation || p.Cwd != "/Users/me/work/api" ||
		!strings.HasSuffix(p.TranscriptPath, "transcript_full.jsonl") {
		t.Errorf("payload = %+v", p)
	}

	// ~/.gemini/config/hooks.json is read by the IDE too. Its conversations
	// are not sessions Agentman lists.
	if p, err := ParsePayload([]byte(agyStop(home, agyConversation, "antigravity-ide"))); err != nil || p.SessionID != "" {
		t.Errorf("an IDE conversation was accepted: %+v %v", p, err)
	}

	// Claude's shape is untouched.
	if p, _ := ParsePayload([]byte(`{"hook_event_name":"Stop","session_id":"abc","cwd":"/w"}`)); p.SessionID != "abc" {
		t.Errorf("claude payload = %+v", p)
	}
}

// A subagent runs hooks too, but it is its parent's work: its Stop must not
// ring the phone for a session the phone does not list.
func TestParseAntigravityPayloadDropsSubagents(t *testing.T) {
	home := t.TempDir()
	const child = "659d2256-2d1f-4ecf-9f18-e0452e64434e"
	record := filepath.Join(home, ".gemini", "antigravity-cli", "brain", agyConversation, ".system_generated", "subagents")
	if err := os.MkdirAll(record, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record, child+".json"), []byte(`{"conversationId":"`+child+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, err := ParsePayload([]byte(agyStop(home, child, "antigravity-cli"))); err != nil || p.SessionID != "" {
		t.Errorf("a subagent's Stop was accepted: %+v %v", p, err)
	}
	if p, _ := ParsePayload([]byte(agyStop(home, agyConversation, "antigravity-cli"))); p.SessionID != agyConversation {
		t.Errorf("the parent's Stop was dropped: %+v", p)
	}
}

// Stop rings the phone, PreInvocation — delivered as UserPromptSubmit — marks
// the session working, and agy gets an empty answer to either, which it
// accepts. Nothing else is taken from it.
func TestServerDeliversAntigravityHooks(t *testing.T) {
	home := t.TempDir()
	server := NewServer("secret")
	post := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
		req.Header.Set("X-Agentman-Token", "secret")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, req)
		return response
	}

	response := post("/hook/antigravity/Stop", agyStop(home, agyConversation, "antigravity-cli"))
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("Stop answered %d %q", response.Code, response.Body.String())
	}
	event := <-server.Events()
	if event.Kind != protocol.KindAntigravity || event.SessionID != "antigravity:"+agyConversation ||
		!event.IsTurnComplete() {
		t.Errorf("event = %+v", event)
	}

	post("/hook/antigravity/UserPromptSubmit", agyStop(home, agyConversation, "antigravity-cli"))
	if state, ok := (<-server.Events()).State(); !ok || state != protocol.StateBusy {
		t.Errorf("PreInvocation state = %q %v", state, ok)
	}

	if got := post("/hook/antigravity/PreToolUse", agyStop(home, agyConversation, "antigravity-cli")).Code; got != http.StatusBadRequest {
		t.Errorf("PreToolUse answered %d", got)
	}
	select {
	case event := <-server.Events():
		t.Errorf("unexpected event %+v", event)
	default:
	}
}

func agyHooksFile(home string) string {
	return filepath.Join(home, ".gemini", "config", "hooks.json")
}

func antigravityPlan(t *testing.T, plans []Plan) Plan {
	t.Helper()
	for _, plan := range plans {
		if plan.Kind == protocol.KindAntigravity {
			return plan
		}
	}
	t.Fatalf("no Antigravity plan in %+v", plans)
	return Plan{}
}

// One key of agy's hooks.json is Agentman's; the user's own hooks stay as
// they were, installing twice changes nothing, and uninstalling removes only
// that key.
func TestAntigravityInstallOwnsOneKey(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "antigravity-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	user := `{"lint-checker": {"PostToolUse": [{"matcher": "run_command", "hooks": [{"type": "command", "command": "./lint.sh"}]}]}}`
	if err := os.MkdirAll(filepath.Dir(agyHooksFile(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agyHooksFile(home), []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	installer := Installer{Home: home, Binary: "/Applications/Agent Man/am"}
	plans, err := installer.Plans("tok", false)
	if err != nil {
		t.Fatal(err)
	}
	plan := antigravityPlan(t, plans)
	if !plan.Changed || plan.Err != nil {
		t.Fatalf("plan = %+v", plan)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(agyHooksFile(home))
	var hooks map[string]map[string][]map[string]any
	if err := json.Unmarshal(raw, &hooks); err != nil {
		t.Fatal(err)
	}
	if _, kept := hooks["lint-checker"]; !kept {
		t.Error("the user's hook was lost")
	}
	ours := hooks["agentman"]
	if len(ours) != 2 || ours["PreToolUse"] != nil {
		t.Fatalf("installed %v", ours)
	}
	if got := ours["Stop"][0]["command"]; got != `'/Applications/Agent Man/am' hook antigravity Stop` {
		t.Errorf("Stop runs %q", got)
	}
	if got := ours["PreInvocation"][0]["command"]; got != `'/Applications/Agent Man/am' hook antigravity UserPromptSubmit` {
		t.Errorf("PreInvocation runs %q", got)
	}
	if strings.Contains(string(raw), "tok") {
		t.Error("the token was written into agy's config")
	}

	again, _ := installer.Plans("tok", false)
	if antigravityPlan(t, again).Changed {
		t.Error("a second install changed the file")
	}

	removal, _ := installer.Plans("tok", true)
	plan = antigravityPlan(t, removal)
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(agyHooksFile(home))
	if strings.Contains(string(raw), "agentman") || !strings.Contains(string(raw), "lint-checker") {
		t.Errorf("after uninstall: %s", raw)
	}
}

// No Antigravity CLI, no plan: nothing to install, and nothing for doctor to
// report missing.
func TestAntigravityInstallNeedsTheCLI(t *testing.T) {
	plans, err := Installer{Home: t.TempDir(), Binary: "/usr/local/bin/am"}.Plans("tok", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range plans {
		if plan.Kind == protocol.KindAntigravity {
			t.Errorf("planned %+v", plan)
		}
	}
}

func TestAntigravityInstallRefusesUnreadableConfig(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "antigravity-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(agyHooksFile(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agyHooksFile(home), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	plans, _ := Installer{Home: home, Binary: "/usr/local/bin/am"}.Plans("tok", false)
	if plan := antigravityPlan(t, plans); plan.Err == nil || plan.Changed {
		t.Errorf("plan = %+v", plan)
	}
}
