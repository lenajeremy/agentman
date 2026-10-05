package source

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// agySwitching discovers a pane-backed session showing pane, with agy's model
// listing (captured from `agy models` on 1.2.17) already read.
func agySwitching(t *testing.T, pane string) (*AntigravitySource, *agyKeys, string) {
	t.Helper()
	home := t.TempDir()
	writeAntigravityConversation(t, home, agConversation, agLinePrompt, agLineReply)
	tmuxPane := tmux.Session{Name: "agentman-antigravity-1-a", PanePID: 900, Cwd: "/work/ws1"}
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/work/ws1", conversations: []string{agConversation}},
	}, tmuxPane)
	listing, err := os.ReadFile("testdata/antigravity-models.txt")
	if err != nil {
		t.Fatal(err)
	}
	s.models.list = func(context.Context) ([]byte, error) { return listing, nil }
	s.models.refresh()
	keys := &agyKeys{panes: []string{pane}, after: map[string][]string{}}
	s.capturePane = keys.capture
	s.keys = keys.keys(true)
	discoverAntigravity(t, s)
	return s, keys, "antigravity:tmux-agentman-antigravity-1-a"
}

// What a pane-backed session offers: the three modes shift+tab cycles, the
// models agy lists in the form the session reports its own, and a model
// switch that also changes agy's default.
func TestAntigravityOffersItsModesAndModels(t *testing.T) {
	s, _, id := agySwitching(t, agyFixture(t, "mode-accept-edits"))
	session := s.sessions[id].meta
	if session.Mode != "accept-edits" || strings.Join(session.Modes, ",") != "default,accept-edits,plan" {
		t.Errorf("mode %q modes %q", session.Mode, session.Modes)
	}
	if len(session.Models) != 14 || session.Models[0] != "Gemini 3.8 Flash (high)" ||
		session.Models[11] != "Claude Sonnet 4.6 (Thinking)" || session.ModelScope != protocol.ModelScopeDefault {
		t.Errorf("models %q scope %q", session.Models, session.ModelScope)
	}
	if session.Model != "Gemini 3.8 Flash (high)" {
		t.Errorf("model %q is not in the form the list uses", session.Model)
	}

	// A default-mode footer is the mode called "default".
	s, _, id = agySwitching(t, agyFixture(t, "mode-default"))
	if mode := s.sessions[id].meta.Mode; mode != "default" {
		t.Errorf("mode %q", mode)
	}
}

// Nothing to switch without a pane to switch it in.
func TestAntigravityOffersNoSwitchesWithoutAPane(t *testing.T) {
	home := t.TempDir()
	writeAntigravityConversation(t, home, agConversation, agLinePrompt, agLineReply)
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/work/api", conversations: []string{agConversation}},
	})
	session := discoverAntigravity(t, s)["antigravity:"+agConversation]
	if len(session.Modes) != 0 || len(session.Models) != 0 || session.ModelScope != "" {
		t.Errorf("session = %+v", session)
	}
}

// shift+tab steps default → accept-edits → plan, captured on agy 1.2.17. One
// press at a time, each read back, until the footer names the mode.
func TestAntigravitySwitchesModeOneStepAtATime(t *testing.T) {
	s, keys, id := agySwitching(t, agyFixture(t, "mode-default"))
	keys.after["BTab"] = []string{agyFixture(t, "mode-accept-edits"), agyFixture(t, "mode-plan")}
	if err := s.SetMode(context.Background(), id, "plan"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(keys.events, ";"); got != "send BTab;send BTab" {
		t.Errorf("keys = %q", got)
	}

	// Already there: nothing pressed.
	s, keys, id = agySwitching(t, agyFixture(t, "mode-plan"))
	if err := s.SetMode(context.Background(), id, "plan"); err != nil || len(keys.events) > 0 {
		t.Errorf("err %v keys %q", err, keys.events)
	}
}

// A press the footer does not show is not followed by another.
func TestAntigravityStopsWhenTheModeDoesNotChange(t *testing.T) {
	s, keys, id := agySwitching(t, agyFixture(t, "mode-default"))
	err := s.SetMode(context.Background(), id, "accept-edits")
	if err == nil || strings.Join(keys.events, ";") != "send BTab" {
		t.Errorf("err %v keys %q", err, keys.events)
	}
}

func TestAntigravityWillNotSwitchOverAQuestionOrPanel(t *testing.T) {
	for _, pane := range []string{"command", "model-picker"} {
		s, keys, id := agySwitching(t, agyFixture(t, "mode-default"))
		keys.panes = []string{agyFixture(t, pane)}
		if err := s.SetMode(context.Background(), id, "plan"); err == nil || len(keys.events) > 0 {
			t.Errorf("%s: mode err %v keys %q", pane, err, keys.events)
		}
		if err := s.SetModel(context.Background(), id, "Gemini 3.8 Flash (low)"); err == nil || len(keys.events) > 0 {
			t.Errorf("%s: model err %v keys %q", pane, err, keys.events)
		}
	}
}

func lowEffort(pane string) string {
	return strings.Replace(pane, "Gemini 3.8 Flash · high", "Gemini 3.8 Flash · low", 1)
}

// agy's own /model with the model's id, confirmed from the footer.
func TestAntigravitySwitchesModel(t *testing.T) {
	idle := agyFixture(t, "mode-default")
	s, keys, id := agySwitching(t, idle)
	keys.after["/model gemini-3.8-flash-low"] = []string{lowEffort(idle)}
	if err := s.SetModel(context.Background(), id, "Gemini 3.8 Flash (low)"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(keys.events, ";"); got != "type /model gemini-3.8-flash-low" {
		t.Errorf("keys = %q", got)
	}

	// The footer never changing is reported.
	s, keys, id = agySwitching(t, idle)
	if err := s.SetModel(context.Background(), id, "Gemini 3.8 Flash (low)"); err == nil ||
		strings.Join(keys.events, ";") != "type /model gemini-3.8-flash-low" {
		t.Errorf("err %v keys %q", err, keys.events)
	}

	// A model agy does not list is not typed.
	s, keys, id = agySwitching(t, idle)
	if err := s.SetModel(context.Background(), id, "Gemini 9 Ultra (max)"); err == nil || len(keys.events) > 0 {
		t.Errorf("err %v keys %q", err, keys.events)
	}
}

// Mid-turn, the command would wait in agy's message queue instead.
func TestAntigravityWillNotSwitchModelMidTurn(t *testing.T) {
	busy := strings.Replace(agyFixture(t, "mode-default"), "? for shortcuts", "esc to cancel", 1)
	s, keys, id := agySwitching(t, busy)
	if err := s.SetModel(context.Background(), id, "Gemini 3.8 Flash (low)"); err == nil || len(keys.events) > 0 {
		t.Errorf("err %v keys %q", err, keys.events)
	}
}

// /model given something it does not take opens agy's picker. It is closed
// — with Escape, while it is visibly open — and the switch reported failed.
func TestAntigravityNeverLeavesTheModelPickerOpen(t *testing.T) {
	idle := agyFixture(t, "mode-default")
	s, keys, id := agySwitching(t, idle)
	keys.after["/model gemini-3.8-flash-low"] = []string{agyFixture(t, "model-picker")}
	keys.escapes = []string{idle}
	err := s.SetModel(context.Background(), id, "Gemini 3.8 Flash (low)")
	if err == nil || strings.Join(keys.events, ";") != "type /model gemini-3.8-flash-low;send Escape" {
		t.Errorf("err %v keys %q", err, keys.events)
	}
}

func TestParseAntigravityModels(t *testing.T) {
	labels, ids := parseAntigravityModels([]byte("Fetching available models...\n" +
		"gemini-3.8-flash-high\tGemini 3.8 Flash (High)\n" +
		"claude-opus-4-6-thinking\tClaude Opus 4.6 (Thinking)\n" +
		"garbage line\n" +
		"gemini-3.8-flash-high\tGemini 3.8 Flash (High)\n"))
	if strings.Join(labels, "|") != "Gemini 3.8 Flash (high)|Claude Opus 4.6 (Thinking)" ||
		ids["Gemini 3.8 Flash (high)"] != "gemini-3.8-flash-high" {
		t.Errorf("labels %q ids %v", labels, ids)
	}
}
