package source

import (
	"os"
	"strings"
	"testing"
)

// The fixture is a real capture of `agy` mid-reply, taken from tmux with
// scrollback while a 1,200-word answer was being written.
func midTurnPane(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/antigravity-pane-midturn.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// This is the whole point: agy writes nothing to disk until the turn ends, so
// without reading the pane there is no reply to show at all.
func TestAntigravityPartialReplyReadsTheAnswerBeingWritten(t *testing.T) {
	got := antigravityPartialReply(midTurnPane(t))
	if got == "" {
		t.Fatal("no reply extracted from a pane that is visibly writing one")
	}
	if !strings.Contains(got, "Every database management system") {
		t.Errorf("the answer's opening is missing:\n%s", got)
	}
	if !strings.Contains(got, "B-Trees vs. LSM-Trees") {
		t.Errorf("the answer's heading is missing:\n%s", got)
	}
}

// The prompt is echoed above the reply. Including it would show the user
// their own message back, inside the agent's answer.
func TestAntigravityPartialReplyLeavesOutThePrompt(t *testing.T) {
	got := antigravityPartialReply(midTurnPane(t))
	if strings.Contains(got, "Write 1200 words comparing") {
		t.Errorf("the echoed prompt leaked into the reply:\n%s", got)
	}
}

// Everything the interface draws around the answer: the composer pinned at
// the bottom, the status bar, the spinner, and the collapsed reasoning
// summary, none of which the transcript records either.
func TestAntigravityPartialReplyStripsTheInterface(t *testing.T) {
	got := antigravityPartialReply(midTurnPane(t))
	for _, chrome := range []string{
		"esc to cancel",
		"? for shortcuts",
		"Thought for",
		"Gemini 3.8 Flash",
	} {
		if strings.Contains(got, chrome) {
			t.Errorf("%q survived into the reply:\n%s", chrome, got)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		if isAntigravitySpinner(strings.TrimSpace(line)) {
			t.Errorf("a spinner line survived: %q", line)
		}
	}
}

// The reply is indented by two spaces in the pane. Left in, every line of
// every streamed answer would render as an indented code block.
func TestAntigravityPartialReplyRemovesThePaneIndent(t *testing.T) {
	got := antigravityPartialReply(midTurnPane(t))
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "  ") {
			t.Errorf("a line kept its pane indent, which reads as code: %q", line)
		}
	}
}

// An idle pane shows the last finished answer, which the transcript already
// carries. Treating that as a new partial would re-send every reply forever.
func TestAntigravityPartialReplyIsEmptyWithNoTurnRunning(t *testing.T) {
	idle := strings.Join([]string{
		"  Antigravity CLI 1.2.12",
		"",
		strings.Repeat("─", 78),
		">",
		strings.Repeat("─", 78),
		"? for shortcuts                                          Gemini 3.8 Flash · high",
	}, "\n")
	if got := antigravityPartialReply(idle); got != "" {
		t.Errorf("extracted %q from a pane with no turn running", got)
	}
}

// A rule the agent wrote is part of the answer; the one the interface draws
// is not. They are told apart by indentation, because the agent's is indented
// with the rest of its text.
func TestAntigravityRuleTestOnlyMatchesTheInterfaces(t *testing.T) {
	if !isAntigravityRule(strings.Repeat("─", 60)) {
		t.Error("a full-width rule was not recognised as interface chrome")
	}
	if isAntigravityRule("──") {
		t.Error("a short run of dashes was taken for a full-width rule")
	}
	if isAntigravityRule("── heading ──") {
		t.Error("text between dashes was taken for a rule")
	}
}
