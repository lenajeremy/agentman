package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/question"
)

// Set AGENTMAN_TEST_CODEX_PANE to a disposable Codex pane whose async
// question is queued. This exercises the actual CLI shortcut across versions.
func TestLiveCodexQueuedQuestionReveal(t *testing.T) {
	name := os.Getenv("AGENTMAN_TEST_CODEX_PANE")
	if name == "" {
		t.Skip("requires a disposable Codex pane with a queued question")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for {
		pane, err := Capture(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if question.CodexQueued(pane) {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("Codex never queued its test question")
		}
		time.Sleep(250 * time.Millisecond)
	}
	pane, err := RevealCodexQuestion(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	first := question.Detect(pane)
	if first == nil || len(first.Options) < 2 {
		t.Fatalf("queued Codex question did not become answerable: %+v", first)
	}
	secondPane, err := RevealCodexQuestion(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	second := question.Detect(secondPane)
	if second == nil || second.Prompt != first.Prompt {
		t.Fatalf("repeated discovery changed the active question: %+v then %+v", first, second)
	}
}

func TestCodexCustomAnswerEchoIsBoundToQuestion(t *testing.T) {
	if !codexAnswerEchoed("› > Which season do you prefer?\n\n  Orange\n\n• Working", "Orange") {
		t.Fatal("submitted custom answer was missed")
	}
	if codexAnswerEchoed("• Codex mentioned Orange\n\n› Ask Codex to do anything", "Orange") {
		t.Fatal("ordinary transcript text was treated as a submitted answer")
	}
}

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

// Every session a test starts must land on the private server TestMain set up
// (see tmuxtest), never on the one the developer is working in. Run from
// inside tmux, a bare tmux command reaches that server through $TMUX.
func TestTestsRunOnAPrivateServer(t *testing.T) {
	requireTmux(t)
	want := os.Getenv(SocketEnv)
	if want == "" {
		t.Fatal("tests are not isolated: " + SocketEnv + " is unset")
	}
	name, _ := newSink(t)
	got, err := run(context.Background(), "display-message", "-p", "-t", name, "#{socket_path}")
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(path string) string {
		if real, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
			return filepath.Join(real, filepath.Base(path))
		}
		return path
	}
	if resolve(strings.TrimSpace(got)) != resolve(want) {
		t.Fatalf("test session ran on %q, not the private server %q", strings.TrimSpace(got), want)
	}
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

// tmux parses its own flags before the text, so a message beginning with a
// dash was read as an option: "- fix it" failed as an invalid flag, and
// "-t 5" was taken as a pane to type into. Every one must arrive as typed.
func TestSendDeliversTextThatStartsWithADash(t *testing.T) {
	requireTmux(t)
	for _, message := range []string{"- fix the bug", "--help me", "-v verbose", "-t 5", "-"} {
		name, out := newSink(t)
		if err := Send(context.Background(), name, message); err != nil {
			t.Fatalf("%q: %v", message, err)
		}
		if got := readSoon(t, out, message); !strings.Contains(got, message) {
			t.Errorf("%q arrived as %q", message, got)
		}
		if err := sendLiteral(context.Background(), name, " "+message); err != nil {
			t.Fatalf("typing %q as an answer: %v", message, err)
		}
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

func TestFailedMultilinePasteDeletesPrivateBuffer(t *testing.T) {
	requireTmux(t)
	name, _ := newSink(t)
	ctx := context.Background()

	if err := pasteMultiline(ctx, name+"-missing", "sensitive\nmessage"); err == nil {
		t.Fatal("paste to a missing target unexpectedly succeeded")
	}
	buffers, err := run(ctx, "list-buffers", "-F", "#{buffer_name}")
	if err != nil && !strings.Contains(err.Error(), "no buffers") {
		t.Fatal(err)
	}
	for _, buffer := range strings.Fields(buffers) {
		if strings.HasPrefix(buffer, "agentman-") {
			t.Fatalf("failed paste left sensitive tmux buffer %q behind", buffer)
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

// A note is opened with Tab, typed as one line, and submitted, in that order,
// and only once each check has accepted the screen.
func TestAnswerWithNoteOpensTypesAndSubmits(t *testing.T) {
	requireTmux(t)
	name, out := newSink(t)
	var checks []string
	check := func(step string) func(string) bool {
		return func(string) bool { checks = append(checks, step); return true }
	}
	err := AnswerWithNote(context.Background(), name, 1, "name it\nprobe-two",
		check("focused"), check("amending"), check("typed"))
	if err != nil {
		t.Fatal(err)
	}
	got := readSoon(t, out, "name it probe-two")
	tab := strings.Index(got, "\t")
	if tab < 0 || tab > strings.Index(got, "name it probe-two") {
		t.Errorf("the note was not typed after Tab: %q", got)
	}
	if strings.Join(checks, ",") != "focused,amending,typed" {
		t.Errorf("checks ran as %v", checks)
	}
}

// A screen that does not show the note line open stops everything: nothing
// is typed and Enter is never pressed.
func TestAnswerWithNoteStopsWhenTheNoteLineDoesNotOpen(t *testing.T) {
	requireTmux(t)
	name, out := newSink(t)
	yes := func(string) bool { return true }
	no := func(string) bool { return false }
	if err := AnswerWithNote(context.Background(), name, 0, "do not type me", yes, no, yes); err == nil {
		t.Fatal("answered although the note line never opened")
	}
	time.Sleep(200 * time.Millisecond)
	if raw, _ := os.ReadFile(out); strings.Contains(string(raw), "do not type me") {
		t.Errorf("the note was typed: %q", raw)
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

func TestProcessTreeSupportsManyAncestryChecksFromOneSnapshot(t *testing.T) {
	const processCount = 5000
	var table strings.Builder
	for pid := 2; pid < processCount+2; pid++ {
		parent := pid - 1
		if pid == 2 {
			parent = 1
		}
		fmt.Fprintf(&table, "%d %d\n", pid, parent)
	}

	processes := parseProcessTree(table.String())
	for descendant := 3; descendant < 14; descendant++ {
		if !processes.OwnsPID(2, descendant) {
			t.Fatalf("pid %d was not recognised as a descendant", descendant)
		}
	}
	if processes.OwnsPID(2, 14) {
		t.Fatal("ancestry deeper than the safety bound was accepted")
	}
	if processes.OwnsPID(4000, 3000) {
		t.Fatal("an ancestor was mistaken for a descendant")
	}
}

func TestProcessTreeParsingIgnoresMalformedRowsAndCycles(t *testing.T) {
	processes := parseProcessTree("header junk\n10 9\n9 8\n8 10\n-1 2\n7 nope\n")
	if !processes.OwnsPID(8, 10) {
		t.Fatal("valid rows around malformed input were lost")
	}
	if processes.OwnsPID(2, 10) {
		t.Fatal("a cycle escaped the bounded ancestry walk")
	}
}

func TestSnapshotProcessTreeHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SnapshotProcessTree(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled snapshot returned %v", err)
	}
}

func BenchmarkProcessTreeOwnsPID(b *testing.B) {
	parents := make(map[int]int, 12)
	for pid := 3; pid <= 13; pid++ {
		parents[pid] = pid - 1
	}
	processes := &ProcessTree{parents: parents}
	b.ResetTimer()
	for b.Loop() {
		if !processes.OwnsPID(2, 13) {
			b.Fatal("ancestry lookup failed")
		}
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

// The command column is the rest of the line, because macOS reports full
// executable paths and some contain spaces. Taking a third field instead cut
// Kiro's bundled runtime down to ".../Application".
func TestProcessTreeKeepsCommandsWithSpaces(t *testing.T) {
	processes := parseProcessTree(
		"59753 59728 /Users/mac/.local/bin/kiro-cli-chat\n" +
			"59728 59547 /Users/mac/Library/Application Support/kiro-cli/bun\n" +
			"  900     1 agy\n" +
			"10 9\n")
	for pid, want := range map[int]string{
		59753: "/Users/mac/.local/bin/kiro-cli-chat",
		59728: "/Users/mac/Library/Application Support/kiro-cli/bun",
		900:   "agy",
		10:    "",
	} {
		if got := processes.Command(pid); got != want {
			t.Errorf("Command(%d) = %q, want %q", pid, got, want)
		}
	}
	// Two-column rows still build the tree, so ancestry keeps working when a
	// platform's ps omits the command.
	if !processes.OwnsPID(9, 10) {
		t.Error("a two-column row was dropped from the tree")
	}
	if !processes.OwnsPID(59547, 59753) {
		t.Error("ancestry broke once rows carried commands")
	}
}
