package source

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// writeCodexModelsCache writes the cache Codex keeps of the models its
// picker lists, in the shape Codex 0.159.3 writes it.
func writeCodexModelsCache(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	levels := `[{"effort":"low"},{"effort":"medium"}]`
	body := `{"fetched_at":"2026-10-05T18:00:00Z","models":[` +
		`{"slug":"gpt-6-astra","display_name":"GPT-6-Astra","visibility":"list","priority":2,"supported_reasoning_levels":` + levels + `},` +
		`{"slug":"gpt-6.1-sol","display_name":"GPT-6.1-Sol","visibility":"list","priority":1,"supported_reasoning_levels":` + levels + `},` +
		`{"slug":"gpt-6-sol","display_name":"GPT-6-Sol","visibility":"list","priority":3,"supported_reasoning_levels":` + levels + `},` +
		`{"slug":"gpt-6-luna","display_name":"GPT-6-Luna","visibility":"list","priority":4,"supported_reasoning_levels":` + levels + `},` +
		`{"slug":"gpt-reserve","display_name":"GPT-Reserve","visibility":"hide","priority":4,"supported_reasoning_levels":` + levels + `},` +
		`{"slug":"gpt-flat","display_name":"GPT-Flat","visibility":"list","priority":5,"supported_reasoning_levels":[]}]}`
	if err := os.WriteFile(filepath.Join(dir, "models_cache.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexsModeAndModelAreReadFromTheLineUnderItsComposer(t *testing.T) {
	for _, screen := range []struct{ fixture, model, mode string }{
		{"codex_idle_default_mode_real_pane.txt", "GPT-6.1-Sol", "default"},
		{"codex_idle_plan_mode_real_pane.txt", "GPT-6.1-Sol", "plan"},
		{"codex_refused_composer_real_pane.txt", "GPT-6.1-Sol", "default"},
		{"codex_model_session_set_real_pane.txt", "GPT-6-Luna", "default"},
	} {
		model, mode, ok := codexFooter(permissionPane(t, screen.fixture))
		if !ok || model != screen.model || mode != screen.mode {
			t.Errorf("%s: %q in %q (%v), want %q in %q", screen.fixture, model, mode, ok, screen.model, screen.mode)
		}
	}
	for _, fixture := range []string{
		"codex_approval_real_pane.txt", "codex_model_picker_real_pane.txt", "codex_effort_picker_real_pane.txt",
	} {
		if model, mode, ok := codexFooter(permissionPane(t, fixture)); ok {
			t.Errorf("%s: read %q in %q with no composer focused", fixture, model, mode)
		}
	}
}

// The models offered are the ones Codex's picker lists, in its order. A
// model with no reasoning levels is left out: the picker would apply it at
// once as the default, with no step to say "this session only".
func TestCodexModelsComeFromCodexsOwnList(t *testing.T) {
	home := t.TempDir()
	writeCodexModelsCache(t, home)
	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for _, model := range src.codexModels() {
		slugs = append(slugs, model.slug)
	}
	if want := []string{"gpt-6.1-sol", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna"}; !slices.Equal(slugs, want) {
		t.Errorf("models %v, want %v", slugs, want)
	}

	session := protocol.Session{Model: "gpt-5.5"}
	src.applyFooter(&session, permissionPane(t, "codex_model_session_set_real_pane.txt"))
	if session.Mode != "default" || !slices.Equal(session.Modes, codexModes) {
		t.Errorf("mode %q of %v", session.Mode, session.Modes)
	}
	if session.ModelScope != protocol.ModelScopeSession || !slices.Equal(session.Models, slugs) {
		t.Errorf("models %v scoped %q", session.Models, session.ModelScope)
	}
	// The footer is what is in use now; the rollout only records a switch at
	// the next turn.
	if session.Model != "gpt-6-luna" {
		t.Errorf("model %q, want the one the footer shows", session.Model)
	}
}

func codexSessionAt(t *testing.T, terminal *fakeTerminal) *CodexSource {
	t.Helper()
	home := t.TempDir()
	writeCodexModelsCache(t, home)
	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	src.switcher = terminal.driver()
	src.sessions["codex:tmux-agentman-codex-s1"] = codexSession{
		meta: protocol.Session{ID: "codex:tmux-agentman-codex-s1"}, tmuxName: "agentman-codex-s1",
	}
	return src
}

func TestCodexSetModeTogglesPlanAndReadsItBack(t *testing.T) {
	plan := false
	terminal := &fakeTerminal{screen: permissionPane(t, "codex_idle_default_mode_real_pane.txt")}
	terminal.onKey = func(key string) string {
		if key == "BTab" {
			plan = !plan
		}
		if plan {
			return permissionPane(t, "codex_idle_plan_mode_real_pane.txt")
		}
		return permissionPane(t, "codex_idle_default_mode_real_pane.txt")
	}
	src := codexSessionAt(t, terminal)
	if err := src.SetMode(context.Background(), "codex:tmux-agentman-codex-s1", "plan"); err != nil {
		t.Fatal(err)
	}
	if err := src.SetMode(context.Background(), "codex:tmux-agentman-codex-s1", "default"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(terminal.keys, []string{"BTab", "BTab"}) {
		t.Errorf("pressed %v", terminal.keys)
	}
}

func TestCodexSwitchesNothingAtAnApproval(t *testing.T) {
	terminal := &fakeTerminal{screen: permissionPane(t, "codex_approval_real_pane.txt")}
	src := codexSessionAt(t, terminal)
	if err := src.SetMode(context.Background(), "codex:tmux-agentman-codex-s1", "plan"); err == nil {
		t.Error("switched mode at an approval")
	}
	if err := src.SetModel(context.Background(), "codex:tmux-agentman-codex-s1", "gpt-6-luna"); err == nil {
		t.Error("switched model at an approval")
	}
	if len(terminal.keys)+len(terminal.lines)+len(terminal.literals) != 0 {
		t.Errorf("sent %v %v %v", terminal.keys, terminal.lines, terminal.literals)
	}
}

var codexPickerRowPrefix = regexp.MustCompile(`^([› ]) (\d+)\. `)

// codexPickerFocusedOn moves the captured model picker's focus to row n.
func codexPickerFocusedOn(t *testing.T, n int) string {
	t.Helper()
	lines := strings.Split(permissionPane(t, "codex_model_picker_real_pane.txt"), "\n")
	inPicker := false
	for index, line := range lines {
		if strings.Contains(line, "Select Model and Effort") {
			inPicker = true
		}
		match := codexPickerRowPrefix.FindStringSubmatch(line)
		if !inPicker || match == nil {
			continue
		}
		number, _ := strconv.Atoi(match[2])
		marker := " "
		if number == n {
			marker = "›"
		}
		lines[index] = marker + line[len(match[1]):]
	}
	return strings.Join(lines, "\n")
}

// Codex saves a model as the default on Enter at its reasoning level, and
// applies it for this session only on "s". Only "s" is pressed there.
func TestCodexSetModelChoosesTheModelForThisSessionOnly(t *testing.T) {
	focus, effort := 1, false
	terminal := &fakeTerminal{screen: permissionPane(t, "codex_idle_default_mode_real_pane.txt")}
	terminal.onLine = func(text string) string {
		if text == "/model" {
			return codexPickerFocusedOn(t, focus)
		}
		return terminal.screen
	}
	terminal.onKey = func(key string) string {
		switch {
		case effort:
			t.Errorf("pressed %s at the reasoning level", key)
		case key == "Down":
			focus = min(focus+1, 8)
		case key == "Up":
			focus = max(focus-1, 1)
		case key == "Enter" && focus == 4:
			effort = true
			return permissionPane(t, "codex_effort_picker_real_pane.txt")
		}
		return codexPickerFocusedOn(t, focus)
	}
	terminal.onLiteral = func(key string) string {
		if effort && key == "s" {
			return permissionPane(t, "codex_model_session_set_real_pane.txt")
		}
		return terminal.screen
	}
	src := codexSessionAt(t, terminal)
	if err := src.SetModel(context.Background(), "codex:tmux-agentman-codex-s1", "gpt-6-luna"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(terminal.keys, []string{"Down", "Down", "Down", "Enter"}) {
		t.Errorf("pressed %v, want the focus moved to GPT-6-Luna and Enter", terminal.keys)
	}
	if !slices.Equal(terminal.literals, []string{"s"}) {
		t.Errorf("chose the reasoning level with %v, want only s", terminal.literals)
	}
}
