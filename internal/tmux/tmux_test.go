package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests drive a real tmux, because the whole point of this package is
// that the interaction with tmux behaves as expected. They are skipped when
// tmux is unavailable so the suite still passes on a machine without it.
func requireTmux(t *testing.T) {
	t.Helper()
	if !Available() {
		t.Skip("tmux is not installed")
	}
}

// newSink starts a session running `cat > file`, so whatever is typed into it
// lands somewhere assertable.
func newSink(t *testing.T) (name, outPath string) {
	t.Helper()
	dir := t.TempDir()
	outPath = filepath.Join(dir, "out.txt")
	name = "agentman-test-" + filepath.Base(dir)

	ctx := context.Background()
	// A shell is needed for the redirection; tmux execs it, so the pane
	// process ends up being cat itself.
	if err := Launch(ctx, name, dir, []string{"sh", "-c", "cat > " + outPath}); err != nil {
		t.Fatalf("launch: %v", err)
	}
	t.Cleanup(func() { _ = Kill(context.Background(), name) })

	waitFor(t, func() bool {
		_, err := os.Stat(outPath)
		return err == nil
	}, "session did not start")
	return name, outPath
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal(msg)
}

func readSoon(t *testing.T, path, want string) string {
	t.Helper()
	var got string
	waitFor(t, func() bool {
		raw, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		got = string(raw)
		return strings.Contains(got, want)
	}, "never received %q; got "+got)
	return got
}

func TestSendDeliversTextLiterally(t *testing.T) {
	requireTmux(t)
	name, out := newSink(t)

	// Shell metacharacters and the literal word "Enter" must all survive: a
	// message is user text, never something to interpret.
	const message = `run $HOME/x.sh && echo "done" ` + "`whoami`" + ` — press Enter`
	if err := Send(context.Background(), name, message); err != nil {
		t.Fatal(err)
	}

	got := readSoon(t, out, "run $HOME")
	if !strings.Contains(got, message) {
		t.Errorf("delivered text was altered:\n got %q\nwant %q", got, message)
	}
}

func TestSendClearsAnExistingDraft(t *testing.T) {
	requireTmux(t)
	name, out := newSink(t)
	ctx := context.Background()

	// Regression: a message used to be typed on top of whatever was already in
	// the prompt box, fusing a half-written draft and the new message into one
	// garbled prompt — observed in the wild as "this is falserun the tests".
	if _, err := run(ctx, "send-keys", "-t", name, "-l", "HALF WRITTEN DRAFT"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	if err := Send(ctx, name, "the real message"); err != nil {
		t.Fatal(err)
	}

	got := readSoon(t, out, "the real message")
	if strings.Contains(got, "HALF WRITTEN DRAFT") {
		t.Errorf("the draft was not cleared before typing; agent received %q", got)
	}
}

func TestSendHandlesMultilineText(t *testing.T) {
	requireTmux(t)
	name, out := newSink(t)

	// Raw newlines would submit at the first line break and scatter the rest
	// across follow-up turns, so multi-line text goes through bracketed paste.
	const message = "first line\nsecond line\nthird line"
	if err := Send(context.Background(), name, message); err != nil {
		t.Fatal(err)
	}

	got := readSoon(t, out, "third line")
	for _, line := range []string{"first line", "second line", "third line"} {
		if !strings.Contains(got, line) {
			t.Errorf("lost %q from a multi-line message; got %q", line, got)
		}
	}
}

func TestSendRejectsEmptyMessages(t *testing.T) {
	requireTmux(t)
	name, _ := newSink(t)
	if err := Send(context.Background(), name, "   \n  "); err == nil {
		t.Error("expected an empty message to be refused rather than submitted")
	}
}

func TestAnswerCustomSelectsTypesAndSubmits(t *testing.T) {
	requireTmux(t)
	name, out := newSink(t)
	if err := AnswerCustom(context.Background(), name, "3", "another answer"); err != nil {
		t.Fatal(err)
	}

	got := readSoon(t, out, "another answer")
	if !strings.Contains(got, "3another answer\n") {
		t.Errorf("custom-answer keystrokes arrived out of order: %q", got)
	}
}

func TestAnswerFormTogglesBeforeTypingAndSubmits(t *testing.T) {
	requireTmux(t)
	name, out := newSink(t)
	if err := AnswerForm(
		context.Background(), name, []string{"1", "2"}, -1, 2, 1, "Desktop",
	); err != nil {
		t.Fatal(err)
	}

	got := readSoon(t, out, "Desktop")
	toggles := strings.Index(got, "12")
	text := strings.Index(got, "Desktop")
	if toggles < 0 || text < 0 || toggles > text || !strings.HasSuffix(got, "\n") {
		t.Errorf("multi-select keystrokes arrived out of order: %q", got)
	}
}

func TestClaudeFormSubmitFocusRequiresVisibleNextMarker(t *testing.T) {
	const nextFocused = `❯ 1. [✔] Alpha
  2. [✔] Bravo
❯    Next
Enter to select · Tab/Arrow keys to navigate · Esc to cancel`
	if !liveFormFooter.MatchString(nextFocused) || !focusedFormControl.MatchString(nextFocused) {
		t.Fatal("real Claude Next-focus shape was not recognized")
	}

	const optionFocused = `❯ 1. [✔] Alpha
  2. [✔] Bravo
     Next
Enter to select · Tab/Arrow keys to navigate · Esc to cancel`
	if !liveFormFooter.MatchString(optionFocused) {
		t.Fatal("real Claude form footer was not recognized")
	}
	if focusedFormControl.MatchString(optionFocused) {
		t.Fatal("an unfocused Next row was considered safe to submit")
	}
}

func TestClaudeQuestionTabHintSurvivesFooterWrapping(t *testing.T) {
	const wrapped = `Enter to select · ↑/↓ to navigate · n to add notes · Tab to
switch questions · ctrl+g to edit in Vim · Esc to cancel`
	if !hasClaudeQuestionTabs(wrapped) {
		t.Fatal("wrapped Tab-to-switch-questions footer was not recognized")
	}
	if hasClaudeQuestionTabs("Enter to select · ↑/↓ to navigate · Esc to cancel") {
		t.Fatal("a standalone menu was mistaken for a multi-question tab form")
	}
}

// Set AGENTMAN_LIVE_CLAUDE_FORM to a tmux session that is genuinely showing
// the four-row Claude checkbox layout captured in the question fixtures. This
// opt-in check exercises the production navigation and safety verification;
// it intentionally answers and advances that live form.
func TestLiveClaudeAnswerForm(t *testing.T) {
	name := os.Getenv("AGENTMAN_LIVE_CLAUDE_FORM")
	if name == "" {
		t.Skip("no live Claude checkbox form supplied")
	}
	if err := AnswerForm(
		context.Background(), name, []string{"1", "3"}, 0, 4, 0, "",
	); err != nil {
		t.Fatal(err)
	}
}

// Set AGENTMAN_LIVE_CLAUDE_SINGLE_FORM to a tmux session showing the first
// question in a genuine Claude tabbed single-select form, with option 1
// focused. This moves to option 2, verifies that focus, selects it with Enter,
// and advances exactly one tab.
func TestLiveClaudeSingleForm(t *testing.T) {
	name := os.Getenv("AGENTMAN_LIVE_CLAUDE_SINGLE_FORM")
	if name == "" {
		t.Skip("no live Claude single-select tab form supplied")
	}
	if err := AnswerSingleForm(context.Background(), name, "2", 1, true); err != nil {
		t.Fatal(err)
	}
}

func TestListOnlyReportsOurSessions(t *testing.T) {
	requireTmux(t)
	ctx := context.Background()

	// A session the user created for their own work must never be typed into.
	foreign := "user-own-session-" + filepath.Base(t.TempDir())
	if err := Launch(ctx, foreign, t.TempDir(), []string{"sh", "-c", "sleep 30"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Kill(context.Background(), foreign) })

	ours, _ := newSink(t)

	sessions, err := List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sawOurs bool
	for _, session := range sessions {
		if session.Name == foreign {
			t.Error("List returned a session agentman does not own")
		}
		if session.Name == ours {
			sawOurs = true
		}
	}
	if !sawOurs {
		t.Error("List did not return our own session")
	}
}

func TestOwnsPIDMatchesDescendants(t *testing.T) {
	requireTmux(t)
	name, _ := newSink(t)

	sessions, err := List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var pane int
	for _, session := range sessions {
		if session.Name == name {
			pane = session.PanePID
		}
	}
	if pane == 0 {
		t.Fatal("could not find the pane pid")
	}

	// Ancestry is how a discovered agent is matched to the tmux session that
	// can type into it, so a pane must claim itself and nothing unrelated.
	if !OwnsPID(pane, pane) {
		t.Error("a pane should own its own process")
	}
	if OwnsPID(pane, os.Getpid()) {
		t.Error("the test process is not inside the pane but was claimed by it")
	}
	if OwnsPID(pane, 1) {
		t.Error("init must never be claimed")
	}
}

func TestNewNameDoesNotCollideWithinOneSecond(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		name := NewName("codex")
		if _, exists := seen[name]; exists {
			t.Fatalf("NewName returned duplicate %q", name)
		}
		seen[name] = struct{}{}
	}
}
