package hook

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// kiroDefault is kiro_default as Kiro CLI 2.27.1 copies it with
// `kiro-cli agent create … --from kiro_default`, its long prompt shortened.
func kiroDefault() (map[string]any, error) {
	agent := map[string]any{}
	err := json.Unmarshal([]byte(`{
  "name": "agentman-base",
  "description": "Default agent",
  "prompt": "# Kiro CLI Default Agent\n\nYou are the default Kiro CLI agent.",
  "mcpServers": {},
  "tools": ["*"],
  "toolAliases": {},
  "allowedTools": [],
  "resources": ["file://AmazonQ.md", "file://AGENTS.md", "file://README.md", "skill://.kiro/skills/*/SKILL.md"],
  "toolsSettings": {},
  "includeMcpJson": true,
  "model": null,
  "permissions": {"rules": []}
}`), &agent)
	return agent, err
}

func kiroPlan(t *testing.T, home string, remove bool) Plan {
	t.Helper()
	plan := Installer{Home: home, Binary: "/opt/homebrew/bin/am"}.PlanKiro(remove, kiroDefault)
	if plan.Err != nil {
		t.Fatal(plan.Err)
	}
	return plan
}

// `am install-hooks --kiro` writes an agent of Agentman's own: kiro_default as
// the installed Kiro defines it, named "agentman", with three hooks that call
// back into am. Nothing else in ~/.kiro changes.
func TestKiroAgentInstallsIntoAFreshHome(t *testing.T) {
	home := t.TempDir()
	plan := kiroPlan(t, home, false)
	if !plan.Changed || plan.Kind != protocol.KindKiro || plan.Path != KiroAgentPath(home) {
		t.Fatalf("plan = %+v", plan)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(KiroAgentPath(home))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("agent file: %v %v", info, err)
	}
	var agent struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
		Tools       []string
		Resources   []string
		Hooks       map[string][]struct {
			Command   string `json:"command"`
			TimeoutMS int    `json:"timeout_ms"`
		} `json:"hooks"`
	}
	raw, _ := os.ReadFile(KiroAgentPath(home))
	if err := json.Unmarshal(raw, &agent); err != nil {
		t.Fatal(err)
	}
	if agent.Name != "agentman" || !strings.HasPrefix(agent.Prompt, "# Kiro CLI Default Agent") ||
		len(agent.Tools) != 1 || len(agent.Resources) != 4 {
		t.Errorf("agent = %+v", agent)
	}
	want := map[string]string{
		"agentSpawn":       "/opt/homebrew/bin/am hook kiro SessionStart",
		"userPromptSubmit": "/opt/homebrew/bin/am hook kiro UserPromptSubmit",
		"stop":             "/opt/homebrew/bin/am hook kiro Stop",
	}
	if len(agent.Hooks) != len(want) {
		t.Errorf("hooks = %+v", agent.Hooks)
	}
	for trigger, command := range want {
		entries := agent.Hooks[trigger]
		if len(entries) != 1 || entries[0].Command != command || entries[0].TimeoutMS != 5000 {
			t.Errorf("%s = %+v, want %q", trigger, entries, command)
		}
	}
	if !KiroAgentInstalled(home) {
		t.Error("the installed agent is not recognised as Agentman's")
	}
	if again := kiroPlan(t, home, false); again.Changed {
		t.Errorf("installing twice changed the file:\n%s", again.After)
	}
}

// Removing takes the file away whole, keeping a backup; removing it again is
// nothing to do.
func TestKiroAgentRemoval(t *testing.T) {
	home := t.TempDir()
	if err := kiroPlan(t, home, false).Apply(); err != nil {
		t.Fatal(err)
	}
	plan := Installer{Home: home}.PlanKiro(true, nil)
	if plan.Err != nil || !plan.Changed || !plan.Delete {
		t.Fatalf("plan = %+v", plan)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(KiroAgentPath(home)); !os.IsNotExist(err) {
		t.Errorf("agent file still there: %v", err)
	}
	if _, err := os.Stat(KiroAgentPath(home) + ".agentman.bak"); err != nil {
		t.Errorf("no backup: %v", err)
	}
	if again := (Installer{Home: home}).PlanKiro(true, nil); again.Changed || again.Err != nil {
		t.Errorf("removing twice = %+v", again)
	}
}

// An agent the user named "agentman" is theirs: never replaced, never
// removed, never started as ours.
func TestKiroAgentLeavesTheUsersOwnAlone(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(home+"/.kiro/agents", 0o700); err != nil {
		t.Fatal(err)
	}
	mine := `{"name":"agentman","description":"my own agent","tools":["*"]}`
	if err := os.WriteFile(KiroAgentPath(home), []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	if plan := (Installer{Home: home, Binary: "am"}).PlanKiro(false, kiroDefault); plan.Err == nil {
		t.Error("install would replace the user's own agent")
	}
	if plan := (Installer{Home: home}).PlanKiro(true, nil); plan.Changed || plan.Delete || plan.Err != nil {
		t.Errorf("removal would touch the user's own agent: %+v", plan)
	}
	if KiroAgentInstalled(home) || KiroAgentArgs(home, nil) != nil {
		t.Error("the user's own agent was taken for Agentman's")
	}
	raw, _ := os.ReadFile(KiroAgentPath(home))
	if string(raw) != mine {
		t.Errorf("file changed: %s", raw)
	}
}

// Kiro's hooks are opt-in: the default plan set never includes them.
func TestKiroIsNeverInTheDefaultPlans(t *testing.T) {
	home := t.TempDir()
	for _, remove := range []bool{false, true} {
		plans, err := Installer{Home: home, Binary: "am"}.Plans("tok", remove)
		if err != nil {
			t.Fatal(err)
		}
		for _, plan := range plans {
			if plan.Kind == protocol.KindKiro {
				t.Errorf("remove=%v: the default plans include Kiro", remove)
			}
		}
	}
}

func TestKiroAgentNeedsKiro(t *testing.T) {
	plan := Installer{Home: t.TempDir(), Binary: "am"}.PlanKiro(false, func() (map[string]any, error) {
		return nil, errors.New("Kiro CLI is not installed (kiro-cli is not on PATH)")
	})
	if plan.Err == nil || plan.Changed {
		t.Errorf("plan = %+v", plan)
	}
}

// Sessions start as Agentman's agent only when it is installed and ours and
// the user chose no agent of their own.
func TestKiroAgentArgs(t *testing.T) {
	home := t.TempDir()
	if args := KiroAgentArgs(home, nil); args != nil {
		t.Errorf("not installed: %q", args)
	}
	if err := kiroPlan(t, home, false).Apply(); err != nil {
		t.Fatal(err)
	}
	if args := KiroAgentArgs(home, nil); strings.Join(args, " ") != "--agent agentman" {
		t.Errorf("installed: %q", args)
	}
	for _, own := range [][]string{{"--agent", "kiro_planner"}, {"--agent=kiro_planner"}} {
		if args := KiroAgentArgs(home, own); args != nil {
			t.Errorf("with %q: %q", own, args)
		}
	}
}

// Kiro's stop carries the reply as "assistant_response"; it is the turn's
// preview, as Claude's last_assistant_message is. Events Kiro never sends
// for Agentman's agent are refused.
func TestKiroStopPayloadIsDelivered(t *testing.T) {
	server := NewServer("tok")
	body := `{"hook_event_name":"stop","cwd":"/work","session_id":"24c1e67e-38dd-4c53-b911-9be60281dd2f",` +
		`"assistant_response":"The secret word is **mango**."}`
	request := httptest.NewRequest(http.MethodPost, "/hook/kiro/Stop", strings.NewReader(body))
	request.Header.Set("X-Agentman-Token", "tok")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	event := <-server.Events()
	if event.SessionID != "kiro:24c1e67e-38dd-4c53-b911-9be60281dd2f" || !event.IsTurnComplete() ||
		event.Preview() != "The secret word is **mango**." {
		t.Fatalf("event = %+v", event)
	}
	for _, name := range []string{"SessionStart", "UserPromptSubmit"} {
		request = httptest.NewRequest(http.MethodPost, "/hook/kiro/"+name,
			strings.NewReader(`{"hook_event_name":"x","cwd":"/work","session_id":"s","prompt":"hi"}`))
		request.Header.Set("X-Agentman-Token", "tok")
		response = httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Errorf("%s: status %d", name, response.Code)
		}
	}
	request = httptest.NewRequest(http.MethodPost, "/hook/kiro/Notification", strings.NewReader(body))
	request.Header.Set("X-Agentman-Token", "tok")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Errorf("an event Kiro never sends was accepted: %d", response.Code)
	}
}
