package manager

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/knowledge"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

const elevenValue = "sk_eleven_SECRET_value_123"

// keyHarness is a harness whose site agent works in a real folder, with an
// ElevenLabs key kept under rule.
func keyHarness(t *testing.T, rule string, projects ...string) (*harness, string) {
	t.Helper()
	h := newHarness(t, &scriptedModel{}, true)
	h.agents.answer = delivered
	folder := t.TempDir()
	sessions := h.agents.Sessions()
	sessions[0].Cwd = folder
	sessions[0].Inject = protocol.InjectTmux
	h.agents.setSessions(sessions)
	event := h.m.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqKeySet, Text: elevenValue,
		Key: &protocol.Key{Name: "elevenlabs_api_key", Description: "Voice", Rule: rule, Projects: projects},
	})
	if event.Type == protocol.EvtError {
		t.Fatal(event.Error)
	}
	return h, folder
}

func gitRepo(t *testing.T, dir string, ignoreEnv bool) {
	t.Helper()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git isn't available: %v %s", err, out)
	}
	if ignoreEnv {
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules\n.env\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readEnv(t *testing.T, dir string) string {
	t.Helper()
	raw, _ := os.ReadFile(filepath.Join(dir, ".env"))
	return string(raw)
}

// The value goes to the Keychain and nowhere else: not the list, not an
// event, not anything the model reads.
func (h *harness) assertValueNeverShown(t *testing.T) {
	t.Helper()
	h.mu.Lock()
	raw, _ := json.Marshal(h.events)
	h.mu.Unlock()
	if strings.Contains(string(raw), elevenValue) {
		t.Fatal("a key's value reached an app")
	}
	entries, _ := h.m.conversation.page("", 500)
	raw, _ = json.Marshal(entries)
	if strings.Contains(string(raw), elevenValue) {
		t.Fatal("a key's value reached the conversation")
	}
}

func TestKeysAreKeptByNameWithTheirValueInTheKeychain(t *testing.T) {
	h, _ := keyHarness(t, "")
	keys := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqKeysList}).Keys
	if len(keys) != 1 || keys[0].Name != "ELEVENLABS_API_KEY" || keys[0].Rule != protocol.KeyAuto || !keys[0].Set {
		t.Fatalf("keys = %+v", keys)
	}
	if h.secrets.values["key:ELEVENLABS_API_KEY"] != elevenValue {
		t.Fatal("the value isn't in the Keychain")
	}
	raw, _ := os.ReadFile(filepath.Join(h.dir, "keys.json"))
	if strings.Contains(string(raw), elevenValue) {
		t.Fatal("the value was written to keys.json")
	}

	// Changing the description keeps the value.
	h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqKeySet, Key: &protocol.Key{Name: "ELEVENLABS_API_KEY", Description: "Voices"}})
	if key, _ := h.m.keys.get("ELEVENLABS_API_KEY"); !key.Set || key.Description != "Voices" {
		t.Fatalf("after an edit: %+v", key)
	}

	for _, bad := range []protocol.Key{{Name: "has space"}, {Name: "ANTHROPIC_API_KEY"}, {Name: "OK", Rule: "sometimes"}, {Name: "OK", Rule: protocol.KeyProjects}} {
		if event := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqKeySet, Key: &bad}); event.Type != protocol.EvtError {
			t.Errorf("saved %+v", bad)
		}
	}
	h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqKeyDelete, Path: "ELEVENLABS_API_KEY"})
	if _, ok := h.secrets.values["key:ELEVENLABS_API_KEY"]; ok {
		t.Fatal("deleting a key left its value in the Keychain")
	}
	h.assertValueNeverShown(t)
}

// An auto key goes straight into the agent's .env, when git won't take it.
func TestAnAutoKeyIsWrittenIntoTheAgentsEnv(t *testing.T) {
	h, folder := keyHarness(t, protocol.KeyAuto)
	gitRepo(t, folder, true)
	if err := os.WriteFile(filepath.Join(folder, ".env"), []byte("PORT=3000\nELEVENLABS_API_KEY=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	site := h.agents.Sessions()[0]
	text, err := h.m.giveKey(context.Background(), site, "ELEVENLABS_API_KEY", "narration")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, elevenValue) || !strings.Contains(text, "ELEVENLABS_API_KEY is now in") {
		t.Fatalf("result = %q", text)
	}
	if env := readEnv(t, folder); env != "PORT=3000\nELEVENLABS_API_KEY="+elevenValue+"\n" {
		t.Fatalf(".env = %q", env)
	}
	if info, _ := os.Stat(filepath.Join(folder, ".env")); info.Mode().Perm() != 0o600 {
		t.Fatalf(".env mode = %v", info.Mode().Perm())
	}
	h.assertValueNeverShown(t)
}

// In a repository that would commit .env, the user is asked first, with
// the fix offered; taking it ignores .env, writes the key and tells the
// agent.
func TestAKeyWaitsUntilGitIgnoresEnv(t *testing.T) {
	h, folder := keyHarness(t, protocol.KeyAuto)
	gitRepo(t, folder, false)
	site := h.agents.Sessions()[0]
	text, err := h.m.giveKey(context.Background(), site, "ELEVENLABS_API_KEY", "narration")
	if err != nil || !strings.Contains(text, "Waiting for the user") {
		t.Fatalf("result = %q, %v", text, err)
	}
	if readEnv(t, folder) != "" {
		t.Fatal("a key was written into a .env git would commit")
	}
	open := h.m.conversation.openCards()
	if len(open) != 1 || open[0].Card.Kind != protocol.CardKey || open[0].Card.Reason != ".env isn't in .gitignore" ||
		open[0].Card.Options[0].Key != "gitignore" {
		t.Fatalf("open = %+v", open)
	}

	reply := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerCardReply, CardID: open[0].Card.ID, OptionKey: "gitignore"})
	if reply.Type == protocol.EvtError {
		t.Fatal(reply.Error)
	}
	ignore, _ := os.ReadFile(filepath.Join(folder, ".gitignore"))
	if string(ignore) != ".env\n" || !strings.Contains(readEnv(t, folder), elevenValue) {
		t.Fatalf(".gitignore = %q, .env = %q", ignore, readEnv(t, folder))
	}
	told := h.agents.sent(protocol.ReqSendMessage)
	if len(told) != 1 || !strings.Contains(told[0].Text, "ELEVENLABS_API_KEY is now in .env") || strings.Contains(told[0].Text, elevenValue) {
		t.Fatalf("the agent was told %+v", told)
	}
	h.assertValueNeverShown(t)
}

// A key the user wants to be asked about waits for them; saying no tells
// the agent and writes nothing.
func TestAnAskKeyWaitsForTheUser(t *testing.T) {
	h, folder := keyHarness(t, protocol.KeyAsk)
	site := h.agents.Sessions()[0]
	if _, err := h.m.giveKey(context.Background(), site, "ELEVENLABS_API_KEY", "narration"); err != nil {
		t.Fatal(err)
	}
	card := h.m.conversation.openCards()[0].Card
	if card.Reason != "you asked to be asked about this key" || card.Options[0].Key != "give" {
		t.Fatalf("card = %+v", card)
	}
	h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerCardReply, CardID: card.ID, OptionKey: "deny"})
	if readEnv(t, folder) != "" {
		t.Fatal("a key the user refused was written")
	}
	if told := h.agents.sent(protocol.ReqSendMessage); len(told) != 1 || !strings.Contains(told[0].Text, "didn't give you") {
		t.Fatalf("the agent was told %+v", told)
	}
}

// A key for some projects goes without asking only to agents in them.
func TestAProjectKeyGoesOnlyToItsProjects(t *testing.T) {
	h, folder := keyHarness(t, protocol.KeyProjects, "/somewhere/else")
	site := h.agents.Sessions()[0]
	if text, _ := h.m.giveKey(context.Background(), site, "ELEVENLABS_API_KEY", ""); !strings.Contains(text, "Waiting") {
		t.Fatalf("outside its projects: %q", text)
	}
	h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqKeySet, Key: &protocol.Key{Name: "ELEVENLABS_API_KEY", Rule: protocol.KeyProjects, Projects: []string{folder}}})
	if text, _ := h.m.giveKey(context.Background(), site, "ELEVENLABS_API_KEY", ""); !strings.Contains(text, "is now in") {
		t.Fatalf("inside its project: %q", text)
	}
}

func TestEnvValuesAreQuotedWhenTheyNeedIt(t *testing.T) {
	for value, want := range map[string]string{
		"sk-abc_123":    "sk-abc_123",
		"has space":     `"has space"`,
		`quote"dollar$`: `"quote\"dollar\$"`,
	} {
		if got := envValue(value); got != want {
			t.Errorf("envValue(%q) = %s, want %s", value, got, want)
		}
	}
}

// Agents reach the shared notes, never the private ones, and their
// requests are matched to the agent working in their folder.
func TestAgentsReadSharedNotesAndAskForKeys(t *testing.T) {
	h, folder := keyHarness(t, protocol.KeyAuto)
	_, _ = h.m.knowledge.Write(knowledge.Note{Tier: knowledge.Shared, Title: "Voice style", Body: "Narrate calmly, British English."})
	_, _ = h.m.knowledge.Write(knowledge.Note{Tier: knowledge.Private, Title: "Voice budget", Body: "Spend at most 20 dollars on voice."})
	ask := func(req protocol.Request) protocol.Event {
		req.Path = filepath.Join(folder, "src")
		return h.m.Handle(context.Background(), req)
	}

	found := ask(protocol.Request{Type: protocol.ReqAgentKnowledgeSearch, Text: "voice"})
	if found.Type != protocol.EvtAgentReply || !strings.Contains(found.Preview, "Voice style") || strings.Contains(found.Preview, "budget") {
		t.Fatalf("search = %+v", found)
	}
	if private := ask(protocol.Request{Type: protocol.ReqAgentKnowledgeRead, Text: "voice-budget"}); private.Type != protocol.EvtError {
		t.Fatalf("an agent read a private note: %+v", private)
	}
	if shared := ask(protocol.Request{Type: protocol.ReqAgentKnowledgeRead, Text: "voice-style"}); !strings.Contains(shared.Preview, "British English") {
		t.Fatalf("read = %+v", shared)
	}
	if keys := ask(protocol.Request{Type: protocol.ReqAgentKeys}); !strings.Contains(keys.Preview, "ELEVENLABS_API_KEY: Voice") {
		t.Fatalf("keys = %+v", keys)
	}
	given := ask(protocol.Request{Type: protocol.ReqAgentRequestKey, Text: "ELEVENLABS_API_KEY", AnswerText: "narration"})
	if !strings.Contains(given.Preview, "is now in") || !strings.Contains(readEnv(t, folder), elevenValue) {
		t.Fatalf("request = %+v, .env = %q", given, readEnv(t, folder))
	}
	var gave bool
	for _, entry := range h.entries() {
		gave = gave || (entry.Role == "activity" && entry.Text == "Gave site ELEVENLABS_API_KEY, in its .env")
	}
	if !gave {
		t.Fatalf("giving the key wasn't shown: %+v", h.entries())
	}
	h.assertValueNeverShown(t)
}

// An agent can tell Agentman something, or ask for the user with a box to
// reply.
func TestAgentsTellAgentman(t *testing.T) {
	h, folder := keyHarness(t, protocol.KeyAuto)
	told := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqAgentTell, Text: "Deployed to staging.", Path: folder})
	if told.Type != protocol.EvtAgentReply {
		t.Fatalf("tell = %+v", told)
	}
	entries := h.entries()
	if last := entries[len(entries)-1]; last.Role != "agent" || last.SessionID != "claude:a" || last.Text != "Deployed to staging." {
		t.Fatalf("entry = %+v", last)
	}
	h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqAgentTell, Text: "Which region, eu or us?", Approve: true, Path: folder})
	open := h.m.conversation.openCards()
	if len(open) != 1 || open[0].Card.Kind != protocol.CardNeedsYou || open[0].Card.Title != "Which region, eu or us?" {
		t.Fatalf("open = %+v", open)
	}
}

// An agent no session matches is still answered, as an agent in its folder.
func TestAnUnknownAgentIsNamedByItsFolder(t *testing.T) {
	h, _ := keyHarness(t, protocol.KeyAuto)
	if s := h.m.agentAt(context.Background(), 0, "/elsewhere/proj"); s.ID != "" || s.Name != "an agent in /elsewhere/proj" {
		t.Fatalf("agent = %+v", s)
	}
}

// Without a key, Agentman isn't running, and agents are told so.
func TestAgentsAreToldWhenAgentmanIsOff(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, false)
	if event := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqAgentKeys, Path: "/w"}); event.Type != protocol.EvtError {
		t.Fatalf("event = %+v", event)
	}
}

// An agent started inside another agent's shell is the one asking, not
// the agent whose shell it is.
func TestTheNearestAgentIsTheOneAsking(t *testing.T) {
	h, _ := keyHarness(t, protocol.KeyAuto)
	sessions := h.agents.Sessions()
	sessions[0].AgentPID = 100 // site, the outer agent
	sessions[1].AgentPID = 200 // api, started from site's shell
	h.agents.setSessions(sessions)
	h.m.processTree = func(context.Context) (*tmux.ProcessTree, error) {
		return tmux.ProcessTreeFromTable("100 1 claude\n150 100 zsh\n200 150 codex\n300 200 am\n400 100 am\n"), nil
	}
	if got := h.m.agentAt(context.Background(), 300, "/anywhere"); got.ID != "codex:b" {
		t.Fatalf("am under api = %s", got.ID)
	}
	if got := h.m.agentAt(context.Background(), 400, "/anywhere"); got.ID != "claude:a" {
		t.Fatalf("am under site = %s", got.ID)
	}
}
