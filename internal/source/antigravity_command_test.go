package source

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// agyCallLine is the model step asking to run command, as agy 1.5.5 writes
// it before its permission prompt.
func agyCallLine(t *testing.T, step int, command string) string {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"step_index": step, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE",
		"created_at": "2026-11-11T17:20:00Z",
		"tool_calls": []map[string]any{{"name": "run_command", "args": map[string]any{"CommandLine": command, "Cwd": "/work/ws1"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

// agyApproving is a pane-backed session showing pane, its transcript the
// lines given, discovered as the phone would be shown it.
func agyApproving(t *testing.T, pane string, lines ...string) (*AntigravitySource, *agyKeys, string) {
	t.Helper()
	home := t.TempDir()
	writeAntigravityConversation(t, home, agConversation, append([]string{agLinePrompt}, lines...)...)
	tmuxPane := tmux.Session{Name: "agentman-antigravity-1-a", PanePID: 900, Cwd: "/work/ws1"}
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/work/ws1", conversations: []string{agConversation}},
	}, tmuxPane)
	keys := &agyKeys{panes: []string{pane}}
	s.capturePane = keys.capture
	s.keys = keys.keys(false)
	id := "antigravity:tmux-agentman-antigravity-1-a"
	if discoverAntigravity(t, s)[id].Question == nil {
		t.Fatal("the fixture shows no question")
	}
	return s, keys, id
}

func fiftyPrints() string {
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = fmt.Sprintf("print(%d)", i+1)
	}
	return `python3 -c "` + strings.Join(lines, "\n") + `"`
}

// agy 1.5.5 showed four lines of a fifty-line command and "⋯ (46 lines
// hidden)": the app showed only those. Whoever approves it reads it whole.
func TestAntigravityShowsAHiddenCommandWhole(t *testing.T) {
	command := fiftyPrints()
	s, keys, id := agyApproving(t, agyFixture(t, "command-hidden-lines"), agyCallLine(t, 1, command))
	shown := s.sessions[id].meta.Question
	if shown.Detail != command {
		t.Fatalf("detail %q", shown.Detail)
	}
	// And it is still answered (the row the cursor is on, which this fake
	// screen needs): the screen is read the same way then.
	if err := agyAnswer(t, s, id, protocol.QuestionAnswer{OptionKey: "1"}); err != nil {
		t.Fatal(err)
	}
	if len(keys.events) != 1 || !strings.HasSuffix(keys.events[0], "enter") {
		t.Errorf("pressed %v", keys.events)
	}
	if q, err := s.CurrentQuestion(context.Background(), id); err != nil || q == nil || q.ID != shown.ID {
		t.Errorf("a fresh look reads another question: %v %+v", err, q)
	}
}

// A long line agy wrapped mid-word ("…terminal-wi" / "ndow-for-sure") is
// shown as the command was written.
func TestAntigravityShowsAWrappedCommandAsWritten(t *testing.T) {
	command := "python3 -c \"import sys; print(1)\"\necho second-line-of-a-long-command-that-goes-on-and-on-and-on-and-on-to-wrap-past-the-edge-of-the-terminal-window-for-sure"
	s, _, id := agyApproving(t, agyFixture(t, "command-wrapped"), agyCallLine(t, 1, command))
	if got := s.sessions[id].meta.Question.Detail; got != command {
		t.Errorf("detail %q", got)
	}
}

// The transcript's call stands in for the screen's only when it is the
// command on screen, and still waiting.
func TestAntigravityKeepsTheScreensCommandOtherwise(t *testing.T) {
	pane := agyFixture(t, "command-hidden-lines")
	onScreen := "python3 -c \"print(1)\nprint(2)\nprint(3)\nprint(4)\n⋯ (46 lines hidden)"
	for name, lines := range map[string][]string{
		"another command":     {agyCallLine(t, 1, "rm -rf build")},
		"the call answered":   {agyCallLine(t, 1, fiftyPrints()), agLineResult},
		"no call":             {agLineReply},
		"a later prompt only": {agyCallLine(t, 1, fiftyPrints()), agLineResult, agLineReply},
	} {
		s, _, id := agyApproving(t, pane, lines...)
		if got := s.sessions[id].meta.Question.Detail; got != onScreen {
			t.Errorf("%s: detail %q", name, got)
		}
	}
}

func TestAntigravityCommandMatching(t *testing.T) {
	command := "a\nb\nc\nd\ne"
	for detail, want := range map[string]bool{
		"a\nb\n⋯ (3 lines hidden)":    true,
		"a\nb\n⋯ (2 lines hidden)\ne": true,
		"a\nb\n⋯ (2 lines hidden)\nx": false,
		"x\n⋯ (4 lines hidden)":       false,
		"a b c d e":                   true,
		"a b c":                       false,
		"⋯ (5 lines hidden)":          false,
	} {
		if got := antigravityShowsCommand(detail, command); got != want {
			t.Errorf("%q: %v, want %v", detail, got, want)
		}
	}
}
