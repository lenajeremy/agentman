package source

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// shift+tab cycles Agent → Plan → Debug → Ask; each press is read back from
// the mode row under the prompt until it names the mode asked for.
func TestCursorCLISetModeCyclesUntilTheRowSaysSo(t *testing.T) {
	s, pane, id := cursorCLIAnswerFixture(t,
		cursorCLIPaneFixture(t, "todos-done"), cursorCLIPaneFixture(t, "plan-mode-idle"),
		cursorCLIPaneFixture(t, "debug-mode-idle"), cursorCLIPaneFixture(t, "ask-mode-idle"))
	if err := s.SetMode(context.Background(), id, "ask"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(pane.typed, "|"); got != "keys:BTab|keys:BTab|keys:BTab" {
		t.Fatalf("typed %q", got)
	}
	if _, _, status := s.cursorCLIPaneReading(context.Background(), "agentman-cursor-test"); status.mode != "ask" {
		t.Fatalf("mode afterwards = %q", status.mode)
	}
}

func TestCursorCLISetModeStopsWhenNothingChanges(t *testing.T) {
	s, pane, id := cursorCLIAnswerFixture(t, cursorCLIPaneFixture(t, "todos-done"))
	if err := s.SetMode(context.Background(), id, "plan"); err == nil {
		t.Fatal("a mode switch that changed nothing reported success")
	}
	if len(pane.typed) != 1 {
		t.Fatalf("kept pressing after shift+tab did nothing: %v", pane.typed)
	}
	// At a decision Cursor ignores shift+tab; nothing is pressed at all.
	s, pane, id = cursorCLIAnswerFixture(t, cursorCLIPaneFixture(t, "delete-approval"))
	if err := s.SetMode(context.Background(), id, "plan"); err == nil || len(pane.typed) != 0 {
		t.Fatalf("switch at a menu: %v, typed %v", err, pane.typed)
	}
}

func modelPalette(command, display string) string {
	return "  Done.\n" +
		" ▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄\n" +
		"  → " + command + "\n" +
		" ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀\n" +
		"   → " + command + "          " + display + " · 300K Medium (Tab to modify)\n" +
		"     /claude-opus-5            Claude Opus 5 · 1M High (Tab to modify)\n"
}

func modelSwitched(display string) string {
	return "  Done.\n  Model: " + display + "\n" +
		" ▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄\n" +
		"  → Add a follow-up\n" +
		" ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀\n" +
		"  " + display + " · 7.4%\n"
}

var opus55 = cursorACPModel{ID: "claude-opus-5-5", Name: "Claude Opus 5.5"}

// The model's command is typed into an empty prompt, Enter is pressed only on
// that exact highlighted row, and the printed "Model:" line is the read-back.
func TestCursorCLISetModelThroughItsCommand(t *testing.T) {
	s, pane, id := cursorCLIAnswerFixture(t, cursorCLIPaneFixture(t, "todos-done"),
		modelPalette("/claude-opus-5-5", "Claude Opus 5.5"), modelSwitched("Claude Opus 5.5"))
	if err := s.SetModelTo(context.Background(), id, opus55); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(pane.typed, "|"); got != "key:/claude-opus-5-5|keys:Enter" {
		t.Fatalf("typed %q", got)
	}
}

// When the list highlights anything else, nothing is selected and the typed
// command is taken back out.
func TestCursorCLISetModelNeverPicksAnotherRow(t *testing.T) {
	wrong := strings.Replace(modelPalette("/claude-opus-5-5", "Claude Opus 5.5"),
		"   → /claude-opus-5-5          Claude Opus 5.5", "   → /claude-opus-5-5          Claude Opus 5", 1)
	s, pane, id := cursorCLIAnswerFixture(t, cursorCLIPaneFixture(t, "todos-done"), wrong)
	if err := s.SetModelTo(context.Background(), id, opus55); err == nil {
		t.Fatal("a row for another model was accepted")
	}
	if got := strings.Join(pane.typed, "|"); got != "key:/claude-opus-5-5|key:"+strings.Repeat("\x7f", 16) {
		t.Fatalf("typed %q", got)
	}
}

// A draft of the user's is never overwritten, and a switch Cursor refuses
// is reported with its own words.
func TestCursorCLISetModelRespectsTheDraftAndCursorsAnswer(t *testing.T) {
	s, pane, id := cursorCLIAnswerFixture(t, cursorCLIPaneFixture(t, "multiline-draft"))
	if err := s.SetModelTo(context.Background(), id, opus55); err == nil || len(pane.typed) != 0 {
		t.Fatalf("switch over a draft: %v, typed %v", err, pane.typed)
	}
	refused := strings.Replace(modelSwitched("Claude Opus 5.5"), "Model: Claude Opus 5.5", "Could not switch to Claude Opus 5.5", 1)
	s, _, id = cursorCLIAnswerFixture(t, cursorCLIPaneFixture(t, "todos-done"),
		modelPalette("/claude-opus-5-5", "Claude Opus 5.5"), refused)
	if err := s.SetModelTo(context.Background(), id, opus55); err == nil || !strings.Contains(err.Error(), "Could not switch") {
		t.Fatalf("refused switch: %v", err)
	}
}

func TestCursorCLIModelCommandNames(t *testing.T) {
	for display, want := range map[string]string{
		"Claude Opus 5.5": "claude-opus-5-5", "GPT-5.6 Sol": "gpt-5-6-sol", "Composer 2.5": "composer-2-5",
		"grok-4.7": "grok-4-7", " Auto ": "auto",
	} {
		if got := cursorCLISlug(display); got != want {
			t.Errorf("%q → %q, want %q", display, got, want)
		}
	}
	got := cursorCLIModelCommands(cursorACPModel{ID: "default", Name: "Auto"})
	if strings.Join(got, ",") != "/auto,/default" {
		t.Errorf("commands = %v", got)
	}
	if got := cursorCLIModelCommands(opus55); strings.Join(got, ",") != "/claude-opus-5-5" {
		t.Errorf("commands = %v", got)
	}
	for description, want := range map[string]bool{
		"Claude Opus 5.5": true, "Claude Opus 5.5 · 300K Medium (Tab to modify)": true,
		"Claude Opus 5.5 (Tab to modify)": true, "Claude Opus 5.5 Thinking": false, "Claude Opus 5": false,
	} {
		if cursorCLIDescribes(description, "Claude Opus 5.5") != want {
			t.Errorf("%q describes Claude Opus 5.5: %v", description, !want)
		}
	}
}

// When a built-in command already has a model's display name, Cursor names
// the model's command after the model instead; the first row is not that
// model's, so it is backed out and the second command tried.
func TestCursorCLISetModelTriesTheModelsOwnName(t *testing.T) {
	auto := cursorACPModel{ID: "default", Name: "Auto"}
	builtin := strings.Replace(modelPalette("/auto", "Auto"), "   → /auto          Auto · 300K Medium (Tab to modify)",
		"   → /auto          Toggle auto-run", 1)
	s, pane, id := cursorCLIAnswerFixture(t, cursorCLIPaneFixture(t, "todos-done"), builtin,
		cursorCLIPaneFixture(t, "todos-done"), modelPalette("/default", "Auto"), modelSwitched("Auto"))
	if err := s.SetModelTo(context.Background(), id, auto); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(pane.typed, "|"); got != "key:/auto|key:\x7f\x7f\x7f\x7f\x7f|key:/default|keys:Enter" {
		t.Fatalf("typed %q", got)
	}
}

// A Cursor whose cycle has no Plan mode comes back round to where it began;
// the switch stops there instead of going round again.
func TestCursorCLISetModeStopsAfterOneCycle(t *testing.T) {
	s, pane, id := cursorCLIAnswerFixture(t,
		cursorCLIPaneFixture(t, "todos-done"), cursorCLIPaneFixture(t, "debug-mode-idle"),
		cursorCLIPaneFixture(t, "ask-mode-idle"), cursorCLIPaneFixture(t, "todos-done"),
		cursorCLIPaneFixture(t, "debug-mode-idle"))
	err := s.SetMode(context.Background(), id, "plan")
	if err == nil || !strings.Contains(err.Error(), "in agent mode") {
		t.Fatalf("err = %v", err)
	}
	if len(pane.typed) != 3 {
		t.Fatalf("pressed %d times: %v", len(pane.typed), pane.typed)
	}
}

// Cursor's own list, fetched without a session, is the list a chat with none
// of its own is offered; a terminal chat is not offered a model whose name
// another shares.
func TestCursorACPCatalogueFromCursorsList(t *testing.T) {
	agent := &fakeACPAgent{reply: func(method string, _ json.RawMessage, _ func(string)) (any, string) {
		if method != "cursor/list_available_models" {
			return nil, "unexpected " + method
		}
		return json.RawMessage(`{"models":[{"value":"default","name":"Auto"},{"value":"claude-opus-5-5","name":"Claude Opus 5.5"},
			{"value":"claude-opus-5-5-thinking","name":"Claude Opus 5.5"},{"value":"x[effort=high]","name":"variant"}]}`), ""
	}}
	s, _, _ := idleACPChat(t, agent)
	if got := s.catalogue(true); len(got) != 0 {
		t.Fatalf("catalogue before any fetch = %v", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	var got []cursorACPModel
	for time.Now().Before(deadline) {
		if got = s.catalogue(true); len(got) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(got) != 3 || got[0].ID != "default" || got[0].Name != "Auto" {
		t.Fatalf("catalogue = %+v", got)
	}
	// Fresh for minutes: no second Cursor process per sweep.
	s.catalogue(true)
	time.Sleep(50 * time.Millisecond)
	if calls := agent.called(); len(calls) != 1 {
		t.Fatalf("Cursor asked %d times: %v", len(calls), calls)
	}
	if ids := cursorCLICommandModels(got); strings.Join(ids, ",") != "default" {
		t.Fatalf("terminal models = %v", ids)
	}
}

// Discovery offers the cycle on a pane whose mode row it read, ACP's modes on
// a phone-launched chat, the models ACP last listed on both, and says a model
// switch is Cursor's new default.
func TestCursorGroupOffersModesAndModels(t *testing.T) {
	group, _, _ := cursorGroupFixture(t)
	for _, st := range group.acp.states() {
		st.mu.Lock()
		st.record.Models = []cursorACPModel{{ID: "default", Name: "Auto"}, opus55}
		st.mu.Unlock()
	}
	sessions := []protocol.Session{
		{ID: cursorCLIPaneIDPrefix + "agentman-cursor-a", Inject: protocol.InjectTmux, Mode: "plan"},
		{ID: cursorCLIChatPrefix + "chat-1", Inject: protocol.InjectNone},
		{ID: cursorACPPrefix + "aaaaaaaa-1111-2222-3333-444444444444", Inject: protocol.InjectAPI},
	}
	group.offerSwitches(sessions)
	pane, chat, acp := sessions[0], sessions[1], sessions[2]
	if strings.Join(pane.Modes, ",") != "agent,plan,debug,ask" || strings.Join(pane.Models, ",") != "default,claude-opus-5-5" ||
		pane.ModelScope != protocol.ModelScopeDefault {
		t.Errorf("pane = %+v", pane)
	}
	if len(chat.Modes) != 0 || len(chat.Models) != 0 || chat.ModelScope != "" {
		t.Errorf("a chat with no pane offered switching: %+v", chat)
	}
	if strings.Join(acp.Modes, ",") != "agent,plan,ask" || len(acp.Models) != 2 || acp.ModelScope != protocol.ModelScopeDefault {
		t.Errorf("acp = %+v", acp)
	}
}

// A terminal chat is switched to a model from the list ACP last gave, by its
// display name, and shows that model until its next reply names one.
func TestCursorGroupSwitchesATerminalChatsModel(t *testing.T) {
	group, _, _ := cursorGroupFixture(t)
	for _, st := range group.acp.states() {
		st.mu.Lock()
		st.record.Models = []cursorACPModel{opus55}
		st.mu.Unlock()
	}
	pane := &fakeCursorPane{screens: []string{cursorCLIPaneFixture(t, "todos-done"),
		modelPalette("/claude-opus-5-5", "Claude Opus 5.5"), modelSwitched("Claude Opus 5.5")}}
	terminal := group.terminal
	terminal.capturePane, terminal.sendKey, terminal.sendText, terminal.sendKeys = pane.capture, pane.key, pane.text, pane.keys
	id := cursorCLIPaneIDPrefix + "agentman-cursor-test"
	terminal.mu.Lock()
	terminal.sessions[id] = cursorCLISession{pane: "agentman-cursor-test",
		meta: protocol.Session{ID: id, LastActivityAt: 100, Model: "cursor-grok-4.5-high-fast"}}
	terminal.mu.Unlock()
	if err := group.SetModel(context.Background(), id, "claude-opus-5-5"); err != nil {
		t.Fatal(err)
	}
	if err := group.SetModel(context.Background(), id, "gpt-9"); err == nil {
		t.Fatal("a model not in Cursor's list was switched to")
	}
	sessions := []protocol.Session{{ID: id, Inject: protocol.InjectTmux, Mode: "agent", LastActivityAt: 100, Model: "cursor-grok-4.5-high-fast"}}
	group.offerSwitches(sessions)
	if sessions[0].Model != "claude-opus-5-5" {
		t.Fatalf("model after the switch = %q", sessions[0].Model)
	}
	sessions[0].LastActivityAt, sessions[0].Model = 200, "claude-opus-5-5-medium"
	group.offerSwitches(sessions)
	if sessions[0].Model != "claude-opus-5-5-medium" {
		t.Fatalf("the next reply's model was hidden: %q", sessions[0].Model)
	}
}
