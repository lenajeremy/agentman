package source

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// fakeTerminal stands in for a pane: it shows one screen, and each key,
// line or literal it is sent moves it to the next screen its handlers say.
type fakeTerminal struct {
	mu        sync.Mutex
	screen    string
	onKey     func(key string) string
	onLine    func(text string) string
	onLiteral func(key string) string
	keys      []string
	lines     []string
	literals  []string
}

func (f *fakeTerminal) driver() paneDriver {
	return paneDriver{
		capture: func(context.Context, string) (string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.screen, nil
		},
		keys: func(_ context.Context, _ string, keys ...string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, key := range keys {
				f.keys = append(f.keys, key)
				if f.onKey != nil {
					f.screen = f.onKey(key)
				}
			}
			return nil
		},
		line: func(_ context.Context, _ string, text string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.lines = append(f.lines, text)
			if f.onLine != nil {
				f.screen = f.onLine(text)
			}
			return nil
		},
		literal: func(_ context.Context, _ string, key string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.literals = append(f.literals, key)
			if f.onLiteral != nil {
				f.screen = f.onLiteral(key)
			}
			return nil
		},
	}
}

// claudeAtPrompt is a Claude session in an agentman pane whose screen is
// the fake terminal.
func claudeAtPrompt(t *testing.T, terminal *fakeTerminal) *ClaudeSource {
	t.Helper()
	src, err := NewClaudeSource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src.switcher = terminal.driver()
	src.sessions["claude:s1"] = claudeSession{
		meta:       protocol.Session{ID: "claude:s1"},
		tmuxName:   "agentman-claude-s1",
		transcript: filepath.Join(t.TempDir(), "none.jsonl"),
	}
	return src
}

// Captured from Claude Code 2.1.289 after each shift+tab.
var claudeModeScreens = []struct{ fixture, mode string }{
	{"claude_mode_auto_real_pane.txt", "auto"},
	{"claude_mode_manual_real_pane.txt", "manual"},
	{"claude_mode_accept_edits_real_pane.txt", "accept edits"},
	{"claude_mode_plan_real_pane.txt", "plan"},
}

func TestClaudesModeIsReadFromTheLineUnderItsPrompt(t *testing.T) {
	for _, screen := range claudeModeScreens {
		if got, ok := claudePaneMode(permissionPane(t, screen.fixture)); !ok || got != screen.mode {
			t.Errorf("%s: mode = %q, %v; want %q", screen.fixture, got, ok, screen.mode)
		}
	}
	// With the model picker open there is no mode line, and nothing may be
	// pressed as if there were.
	if mode, ok := claudePaneMode(permissionPane(t, "claude_model_picker_real_pane.txt")); ok {
		t.Errorf("read mode %q from the model picker", mode)
	}
}

func cyclingClaude(t *testing.T, start int) *fakeTerminal {
	t.Helper()
	at := start
	terminal := &fakeTerminal{screen: permissionPane(t, claudeModeScreens[at].fixture)}
	terminal.onKey = func(key string) string {
		if key == "BTab" {
			at = (at + 1) % len(claudeModeScreens)
		}
		return permissionPane(t, claudeModeScreens[at].fixture)
	}
	return terminal
}

func TestSetModePressesShiftTabUntilClaudeShowsTheMode(t *testing.T) {
	terminal := cyclingClaude(t, 0) // auto
	src := claudeAtPrompt(t, terminal)
	if err := src.SetMode(context.Background(), "claude:s1", "plan"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(terminal.keys, []string{"BTab", "BTab", "BTab"}) {
		t.Errorf("pressed %v, want shift+tab three times (auto → manual → accept edits → plan)", terminal.keys)
	}
	terminal.keys = nil
	if err := src.SetMode(context.Background(), "claude:s1", "plan"); err != nil || len(terminal.keys) != 0 {
		t.Errorf("switching to the mode already on pressed %v (%v)", terminal.keys, err)
	}
}

func TestSetModeStopsWhenTheModeNeverShows(t *testing.T) {
	terminal := cyclingClaude(t, 0)
	src := claudeAtPrompt(t, terminal)
	if err := src.SetMode(context.Background(), "claude:s1", "bypass permissions"); err == nil {
		t.Fatal("a mode Claude never showed was reported switched")
	}
	if len(terminal.keys) > len(claudeModes)+2 {
		t.Errorf("pressed %d keys looking for it", len(terminal.keys))
	}
}

func TestSetModePressesNothingWhileAPickerIsOpen(t *testing.T) {
	terminal := &fakeTerminal{screen: permissionPane(t, "claude_model_picker_real_pane.txt")}
	src := claudeAtPrompt(t, terminal)
	if err := src.SetMode(context.Background(), "claude:s1", "plan"); err == nil {
		t.Error("switched with the model picker open")
	}
	if len(terminal.keys) != 0 {
		t.Errorf("pressed %v into a picker", terminal.keys)
	}
}

var claudePickerRowPrefix = regexp.MustCompile(`^  ([❯↓ ]) ( ?\d+)\.`)

// claudePickerFocusedOn moves the captured picker's focus marker to row n.
func claudePickerFocusedOn(t *testing.T, n int) string {
	t.Helper()
	lines := strings.Split(permissionPane(t, "claude_model_picker_real_pane.txt"), "\n")
	for index, line := range lines {
		match := claudePickerRowPrefix.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		marker := match[1]
		var number int
		fmt.Sscanf(strings.TrimSpace(match[2]), "%d", &number)
		switch {
		case number == n:
			marker = "❯"
		case marker == "❯":
			marker = " "
		}
		lines[index] = "  " + marker + line[len("  ")+len(match[1]):]
	}
	return strings.Join(lines, "\n")
}

func modelPickingClaude(t *testing.T) *fakeTerminal {
	t.Helper()
	focus := 1
	terminal := &fakeTerminal{screen: permissionPane(t, "claude_mode_manual_real_pane.txt")}
	terminal.onLine = func(text string) string {
		if text == "/model" {
			return claudePickerFocusedOn(t, focus)
		}
		return terminal.screen
	}
	terminal.onKey = func(key string) string {
		switch key {
		case "Down":
			focus = min(focus+1, 10)
		case "Up":
			focus = max(focus-1, 1)
		case "Escape":
			return permissionPane(t, "claude_mode_manual_real_pane.txt")
		}
		return claudePickerFocusedOn(t, focus)
	}
	terminal.onLiteral = func(key string) string {
		if key == "s" && focus == 4 {
			return permissionPane(t, "claude_model_session_set_real_pane.txt")
		}
		return terminal.screen
	}
	return terminal
}

// Claude's picker saves a pick as the default for new sessions on Enter,
// and uses it for this session only on "s". Only "s" is ever pressed.
func TestSetModelPicksTheRowAndUsesItForThisSessionOnly(t *testing.T) {
	terminal := modelPickingClaude(t)
	src := claudeAtPrompt(t, terminal)
	if err := src.SetModel(context.Background(), "claude:s1", "claude-sonnet-5-5"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(terminal.lines, []string{"/model"}) {
		t.Errorf("typed %v", terminal.lines)
	}
	if !slices.Equal(terminal.keys, []string{"Down", "Down", "Down"}) {
		t.Errorf("pressed %v, want the focus moved from row 1 to row 4", terminal.keys)
	}
	if !slices.Equal(terminal.literals, []string{"s"}) {
		t.Errorf("chose with %v, want only s", terminal.literals)
	}
	if slices.Contains(terminal.keys, "Enter") {
		t.Error("Enter would have made it the default for new sessions")
	}
	// The transcript says nothing of the switch until Claude next replies,
	// so the session reports it from what was switched.
	if model, ok := src.switched("claude:s1", src.sessions["claude:s1"].transcript); !ok || model != "claude-sonnet-5-5" {
		t.Errorf("after the switch the session reports %q, %v", model, ok)
	}
}

func TestSetModelBacksOutOfThePickerWhenTheModelIsNotListed(t *testing.T) {
	terminal := modelPickingClaude(t)
	src := claudeAtPrompt(t, terminal)
	if err := src.SetModel(context.Background(), "claude:s1", "claude-opus-9-9"); err == nil {
		t.Fatal("a model the picker does not list was reported switched")
	}
	if len(terminal.literals) != 0 {
		t.Errorf("chose %v in the picker", terminal.literals)
	}
	if !slices.Contains(terminal.keys, "Escape") {
		t.Error("the picker was left open")
	}
	if _, ok := claudePaneMode(terminal.screen); !ok {
		t.Error("the prompt did not come back")
	}
}

func TestClaudeModelIDsAndPickerLabels(t *testing.T) {
	for id, label := range map[string]string{
		"claude-opus-5-5":           "Opus 5.5",
		"claude-sonnet-5":           "Sonnet 5",
		"claude-haiku-4-5-20251001": "Haiku 4.5",
		"claude-fable-5-1[1m]":      "Fable 5.1",
	} {
		if got := claudeModelLabel(id); got != label {
			t.Errorf("label(%q) = %q, want %q", id, got, label)
		}
	}
	if got := claudeModelID("Sonnet 5.5"); got != "claude-sonnet-5-5" {
		t.Errorf("id(Sonnet 5.5) = %q", got)
	}
	if claudeModelID("Default (recommended)") != "" || claudeModelLabel("<synthetic>") != "" {
		t.Error("a row that is not a model was given an id")
	}
}

// A pane-bound session says which mode it is in, what it can switch to,
// and that a model switch touches this session alone.
func TestClaudeDiscoveryOffersModesAndModels(t *testing.T) {
	home := t.TempDir()
	src, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	pane := tmux.Session{Name: tmux.Prefix + "claude-11111111-1111-4111-8111-111111111111", Cwd: home, PanePID: 1}
	src.listPanes = func(context.Context) ([]tmux.Session, error) { return []tmux.Session{pane}, nil }
	src.snapshotProcesses = func(context.Context) (*tmux.ProcessTree, error) { return nil, nil }
	src.processArgs = func(context.Context, []int) map[int]string { return nil }
	src.capturePane = func(context.Context, string) (string, error) {
		return permissionPane(t, "claude_mode_accept_edits_real_pane.txt"), nil
	}
	found, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("found %d sessions", len(found))
	}
	session := found[0]
	if session.Mode != "accept edits" || !slices.Equal(session.Modes, claudeModes) {
		t.Errorf("mode %q of %v", session.Mode, session.Modes)
	}
	if session.ModelScope != protocol.ModelScopeSession || !slices.Contains(session.Models, "claude-sonnet-5-5") {
		t.Errorf("models %v scoped %q", session.Models, session.ModelScope)
	}
}
