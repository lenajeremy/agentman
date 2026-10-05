package source

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
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

// After a tool call the model writes its reply to the call's result beneath
// the call's row. That reply streams too, without the row, the thinking
// summary, the spinner or the tip around it. Captured mid-reply on 1.2.17.
func TestAntigravityPartialReplyAfterAToolCall(t *testing.T) {
	raw, err := os.ReadFile("testdata/antigravity-pane-stream-after-tool.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := antigravityPartialReply(string(raw))
	if !strings.HasPrefix(got, "The ls command executed successfully") ||
		!strings.HasSuffix(got, "### The Case for Disposable Research Wo") {
		t.Errorf("reply:\n%s", got)
	}
	for _, chrome := range []string{"● Bash", "Thought for", "Tip:", "Initial focus", "esc to cancel", "Run the shell command"} {
		if strings.Contains(got, chrome) {
			t.Errorf("%q survived into the reply:\n%s", chrome, got)
		}
	}
}

// A tool call drawn with nothing after it yet is not a reply, however much
// is drawn around it: its wrapped tail, a spinner, a subagent's status row
// beneath the prompt.
func TestAntigravityPartialReplyIsEmptyAfterABareToolCall(t *testing.T) {
	raw, err := os.ReadFile("testdata/antigravity-pane-stream-tool-row-only.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := antigravityPartialReply(string(raw)); got != "" {
		t.Errorf("extracted %q", got)
	}
}

// agyFollow follows a pane-backed session whose transcript is path and whose
// pane shows whatever pane() returns.
func agyFollow(t *testing.T, lines []string, pane func() string) (*AntigravitySource, string, string, chan []protocol.Message, func()) {
	t.Helper()
	home := t.TempDir()
	path := writeAntigravityConversation(t, home, agConversation, lines...)
	tmuxPane := tmux.Session{Name: "agentman-antigravity-1-a", PanePID: 900, Cwd: "/work/ws1"}
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/work/ws1", conversations: []string{agConversation}},
	}, tmuxPane)
	s.captureScrollback = func(context.Context, string, int) (string, error) { return pane(), nil }
	id := "antigravity:tmux-agentman-antigravity-1-a"
	discoverAntigravity(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	out := make(chan []protocol.Message, 16)
	done := make(chan error, 1)
	go func() { done <- s.Follow(ctx, id, out) }()
	return s, path, id, out, func() { cancel(); <-done }
}

func nextBatch(t *testing.T, out chan []protocol.Message) []protocol.Message {
	t.Helper()
	select {
	case batch := <-out:
		return batch
	case <-time.After(3 * time.Second):
		t.Fatal("nothing streamed")
		return nil
	}
}

const agStreamingPane = "> list the files\n\n  There are three files here:\n" +
	"────────────────────────────────────────\n>\n────────────────────────────────────────\n" +
	"esc to cancel                                   Gemini 3.8 Flash · high\n"

// A preview whose step lands with no text — the model went on to call a tool
// — is taken back, so it does not stay in the feed as a reply nobody wrote.
func TestAntigravityTakesBackAPreviewNoRecordReplaces(t *testing.T) {
	var mu sync.Mutex
	pane := agStreamingPane
	_, path, _, out, stop := agyFollow(t, []string{agLinePrompt}, func() string {
		mu.Lock()
		defer mu.Unlock()
		return pane
	})
	defer stop()

	preview := nextBatch(t, out)
	if len(preview) != 1 || preview[0].ID != "s000001" || preview[0].Text != "There are three files here:" {
		t.Fatalf("preview = %+v", preview)
	}

	mu.Lock()
	pane = strings.Replace(agStreamingPane, "  There are three files here:\n", "● Bash(ls -la) (ctrl+o to expand)\n", 1)
	mu.Unlock()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString(agLineCall + "\n")
	file.Close()

	batch := nextBatch(t, out)
	var call, withdrawn bool
	for _, m := range batch {
		call = call || (m.ID == "s000002" && m.Tool != nil)
		withdrawn = withdrawn || (m.ID == "s000001" && m.Text == "" && m.Role == protocol.RoleAssistant)
	}
	if !call || !withdrawn {
		t.Errorf("batch = %+v", batch)
	}
}

// A prompt followed by a system message — a subagent's or a background task's
// news, delivered with it — is still a turn whose reply is being written.
func TestAntigravityStreamsAfterASystemMessage(t *testing.T) {
	system := `{"step_index": 1, "source": "SYSTEM", "type": "SYSTEM_MESSAGE", "status": "DONE", "created_at": "2026-09-27T17:02:00Z", "content": "<SYSTEM_MESSAGE>[Notice] All your subagents and background tasks have been stopped</SYSTEM_MESSAGE>"}`
	_, _, _, out, stop := agyFollow(t, []string{agLinePrompt, system}, func() string { return agStreamingPane })
	defer stop()
	if batch := nextBatch(t, out); len(batch) != 1 || batch[0].ID != "s000002" {
		t.Errorf("batch = %+v", batch)
	}
}

// The transcript can wait for a response that is never coming — a turn
// interrupted with Esc writes nothing. The pane is the evidence: idle, it
// holds the last finished answer, which must not stream as a new one.
func TestAntigravityDoesNotStreamFromAnIdlePane(t *testing.T) {
	idle := strings.Replace(agStreamingPane, "esc to cancel", "? for shortcuts", 1)
	_, _, _, out, stop := agyFollow(t, []string{agLinePrompt}, func() string { return idle })
	defer stop()
	select {
	case batch := <-out:
		t.Errorf("streamed %+v from an idle pane", batch)
	case <-time.After(4 * followInterval):
	}
}
