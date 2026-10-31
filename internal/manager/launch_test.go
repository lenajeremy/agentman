package manager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// launchHarness is a harness whose home holds a project, with site working
// in it, and a daemon that starts, ends and reopens sessions, lists site's
// changes and pages its messages.
func launchHarness(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t, &scriptedModel{}, true)
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(home, "code", "site")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	h.agents.setSessions([]protocol.Session{
		{ID: "claude:a", Kind: "claude", Name: "site", Cwd: project, State: protocol.StateIdle, Inject: protocol.InjectTmux},
		{ID: "codex:b", Kind: "codex", Name: "api", Cwd: filepath.Join(home, "code", "api"), State: protocol.StateIdle, Inject: protocol.InjectTmux},
		{ID: "claude:old", Kind: "claude", Name: "old", Cwd: project, State: protocol.StateEnded},
	})
	h.agents.answer = func(req protocol.Request) protocol.Event {
		switch req.Type {
		case protocol.ReqStartSession:
			return protocol.Event{Type: protocol.EvtSessionStarted, SessionID: "codex:tmux-am-codex-1"}
		case protocol.ReqEndSession:
			if req.SessionID == "codex:b" {
				return protocol.Event{Type: protocol.EvtError, Error: "source: this session is not running in a pane Agentman started"}
			}
			return protocol.Event{Type: protocol.EvtSessionEnded, SessionID: req.SessionID}
		case protocol.ReqResumeSession:
			return protocol.Event{Type: protocol.EvtSessionStarted, SessionID: "claude:old"}
		case protocol.ReqSendMessage:
			return protocol.Event{Type: protocol.EvtSendResult, Status: protocol.StatusDelivered}
		case protocol.ReqFetchMessages:
			return protocol.Event{Type: protocol.EvtPage, Page: &protocol.Page{SessionID: req.SessionID, Messages: []protocol.Message{
				{ID: "1", Role: protocol.RoleUser, Text: "Fix sign-in on Safari"},
				{ID: "2", Role: protocol.RoleAssistant, Text: "Swapped in the UUID polyfill; the login test still fails on \x1b[31mSafari 15\x1b[0m."},
				{ID: "3", Role: protocol.RoleTool, Tool: &protocol.Tool{Name: "Bash", Summary: "npm test"}},
			}}}
		case protocol.ReqListChanges:
			return protocol.Event{Type: protocol.EvtWorkspace, Workspace: &protocol.WorkspaceResult{
				Kind: "changes", Changes: []protocol.WorkspaceChange{{Path: "src/login.tsx", Status: " M", Added: 3, Removed: 1}},
			}}
		}
		return protocol.Event{Type: protocol.EvtError, Error: "not in this test"}
	}
	return h, home
}

func TestStartAgentOpensItInTheFolderAndWatchesIt(t *testing.T) {
	h, _ := launchHarness(t)
	result := h.m.runTool(context.Background(), "start_agent", []byte(`{"kind":"codex","folder":"~/code/site","task":"Add rate limiting to the uploads"}`))
	if result.isError {
		t.Fatalf("start_agent: %s", result.text)
	}
	started := h.agents.sent(protocol.ReqStartSession)
	if len(started) != 1 {
		t.Fatalf("started %d sessions", len(started))
	}
	req := started[0]
	if req.Kind != protocol.KindCodex || req.Path != "code/site" || req.Text != "Add rate limiting to the uploads" || req.ClientID == "" {
		t.Errorf("start request = %+v", req)
	}
	watches := h.m.schedules.list()
	if len(watches) != 1 || watches[0].SessionID != "codex:tmux-am-codex-1" || watches[0].Condition != protocol.WatchTurnEnd {
		t.Errorf("watches = %+v", watches)
	}
	if result.activity != "Started Codex in ~/code/site: “Add rate limiting to the uploads”" {
		t.Errorf("activity = %q", result.activity)
	}
}

func TestStartAgentFindsTheFolderByAgentAndPath(t *testing.T) {
	h, home := launchHarness(t)
	for _, folder := range []string{"site", filepath.Join(home, "code", "site"), "code/site"} {
		input := `{"kind":"claude","folder":"` + folder + `","task":"Write the changelog","watch":false}`
		if result := h.m.runTool(context.Background(), "start_agent", []byte(input)); result.isError {
			t.Errorf("folder %q: %s", folder, result.text)
		}
	}
	for _, req := range h.agents.sent(protocol.ReqStartSession) {
		if req.Path != "code/site" || req.Kind != protocol.KindClaude {
			t.Errorf("start request = %+v", req)
		}
	}
	if watches := h.m.schedules.list(); len(watches) != 0 {
		t.Errorf("watch false still watched: %+v", watches)
	}
	for _, folder := range []string{"/etc", "~/code/missing", "~", "../outside"} {
		input := `{"kind":"claude","folder":"` + folder + `","task":"x"}`
		if result := h.m.runTool(context.Background(), "start_agent", []byte(input)); !result.isError {
			t.Errorf("folder %q was accepted", folder)
		}
	}
	if result := h.m.runTool(context.Background(), "start_agent", []byte(`{"kind":"gpt","folder":"site","task":"x"}`)); !result.isError {
		t.Error("an unknown kind was accepted")
	}
}

func TestAHandoffCarriesTheFirstAgentsWork(t *testing.T) {
	h, _ := launchHarness(t)
	if _, err := h.m.briefs.put(protocol.Brief{
		SessionID: "claude:a", Name: "site", Goal: "Fix Safari sign-in",
		Done: []string{"Found the crypto.randomUUID call", "Added the polyfill"}, Now: "Running the login tests",
	}, "2"); err != nil {
		t.Fatal(err)
	}
	result := h.m.runTool(context.Background(), "start_agent", []byte(`{"kind":"codex","folder":"site","task":"Carry on and get the login tests passing","continue_from":"site"}`))
	if result.isError {
		t.Fatalf("start_agent: %s", result.text)
	}
	text := h.agents.sent(protocol.ReqStartSession)[0].Text
	for _, want := range []string{
		"Carry on and get the login tests passing\n\n",
		"another agent, site (Claude), was doing in",
		"Its goal: Fix Safari sign-in",
		"- Found the crypto.randomUUID call",
		"Where it was: Running the login tests",
		"The user's last request to it: “Fix sign-in on Safari”",
		"Its last message: “Swapped in the UUID polyfill; the login test still fails on [31mSafari 15[0m.”",
		"- src/login.tsx (+3 −1)",
		"read the files",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the handoff lacks %q:\n%s", want, text)
		}
	}
	if strings.ContainsRune(text, '\x1b') {
		t.Error("a control character reached the new agent's terminal")
	}
	if !strings.Contains(result.activity, "to carry on site's work") {
		t.Errorf("activity = %q", result.activity)
	}

	// To an agent already running, by message.
	sent := h.m.runTool(context.Background(), "send_to_agent", []byte(`{"agent":"api","text":"Take this over","continue_from":"site"}`))
	if sent.isError {
		t.Fatalf("send_to_agent: %s", sent.text)
	}
	if message := h.agents.sent(protocol.ReqSendMessage)[0].Text; !strings.HasPrefix(message, "Take this over\n\nYou're carrying on") {
		t.Errorf("message = %q", message)
	}
	if sent.activity != "Handed site's work to api: “Take this over”" {
		t.Errorf("activity = %q", sent.activity)
	}
	if self := h.m.runTool(context.Background(), "send_to_agent", []byte(`{"agent":"site","text":"x","continue_from":"site"}`)); !self.isError {
		t.Error("an agent was handed its own work")
	}
}

func TestEndAndReopenAnAgent(t *testing.T) {
	h, _ := launchHarness(t)
	ended := h.m.runTool(context.Background(), "end_agent", []byte(`{"agent":"site"}`))
	if ended.isError || ended.activity != "Ended site" {
		t.Fatalf("end_agent: %+v", ended)
	}
	if sent := h.agents.sent(protocol.ReqEndSession); len(sent) != 1 || sent[0].SessionID != "claude:a" {
		t.Errorf("end requests = %+v", sent)
	}
	// One in the user's own terminal: theirs to close.
	own := h.m.runTool(context.Background(), "end_agent", []byte(`{"agent":"api"}`))
	if !own.isError || !strings.Contains(own.text, "the user's own terminal") {
		t.Errorf("end_agent on the user's terminal: %+v", own)
	}
	if again := h.m.runTool(context.Background(), "end_agent", []byte(`{"agent":"old"}`)); again.isError || len(h.agents.sent(protocol.ReqEndSession)) != 2 {
		t.Errorf("an ended agent was ended again: %+v", again)
	}
	reopened := h.m.runTool(context.Background(), "reopen_agent", []byte(`{"agent":"old"}`))
	if reopened.isError || reopened.activity != "Reopened old" {
		t.Errorf("reopen_agent: %+v", reopened)
	}
}
