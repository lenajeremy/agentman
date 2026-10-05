package source

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// Panes captured from Kiro CLI 2.27.1 at 80x24 while it worked, with the
// scratch directory's path replaced by /work.
func readKiroPane(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestKiroPartialReplyReadsTheReplyBeingWritten(t *testing.T) {
	got := kiroPartialReply(readKiroPane(t, "kiro-pane-streaming-reply.txt"))
	if !strings.HasPrefix(got, "Lighthouses: Beacons of the Edge\n\nThere is something deeply human about a lighthouse.") ||
		!strings.HasSuffix(got, "Standing perhaps 100 meters tall, it was one") {
		t.Fatalf("partial reply = %q", got)
	}
	if strings.Contains(got, "Kiro is working") || strings.Contains(got, "kiro_default ·") ||
		strings.Contains(got, "Write a 600 word essay") {
		t.Errorf("the prompt or Kiro's own lines leaked into the reply: %q", got)
	}
	// Ctrl+S prints a notice under the reply; the reply is still the reply.
	got = kiroPartialReply(readKiroPane(t, "kiro-pane-streaming-then-notice.txt"))
	if !strings.HasPrefix(got, "The sea rolls in with ancient grace,") || !strings.HasSuffix(got, "At dusk the water turns") {
		t.Errorf("reply above a notice = %q", got)
	}
}

// The last block on screen is often a call, not words: a call taking shape
// ("• Write"), a task-list event, a call awaiting approval ("↓ …"), a call
// with its parameters. None of them is a reply.
func TestKiroPartialReplyIgnoresCalls(t *testing.T) {
	for _, name := range []string{"kiro-pane-call-forming.txt", "kiro-pane-task-list-thinking.txt",
		"kiro-pane-subagent-approval.txt", "kiro-pane-parallel-calls.txt"} {
		if got := kiroPartialReply(readKiroPane(t, name)); got != "" {
			t.Errorf("%s: read %q as a reply", name, got)
		}
	}
	// After the calls' results, the next message is the reply.
	after := "────────────────────────────────────────\n  look at red.png\n\n" +
		"• Glob \"**/*.png\"\n    ╰ path=/work\n• Read image /work/red.png\n\n" +
		"• Here are the results of all four steps:\n\n  1. ls -la — The directory contains\n\n" +
		"────────────────────────────────────────\nkiro_default · auto · ◔ 1%   /work\n\n" +
		"›  Kiro is working · 9s · Type to steer · Ctrl+S to queue\n"
	if got := kiroPartialReply(after); got != "Here are the results of all four steps:\n\n1. ls -la — The directory contains" {
		t.Errorf("reply after calls = %q", got)
	}
}

// Follow sends the reply as it is written, under the id the finished text
// will carry, so the record replaces it in place when Kiro writes it.
func TestKiroFollowStreamsTheReplyAndSettlesIt(t *testing.T) {
	home := t.TempDir()
	pid := os.Getpid()
	path := writeKiroSession(t, home, "s", "/work", pid, kiroLinePrompt)
	pane := tmux.Session{Name: "agentman-kiro-1-a", PanePID: pid, Cwd: "/work"}
	s := newTestKiro(t, home, "", pane)
	s.snapshotProcesses = func(context.Context) (*tmux.ProcessTree, error) {
		return tmux.ProcessTreeFromTable(""), nil
	}
	s.listPanes = func(context.Context) ([]tmux.Session, error) { return []tmux.Session{pane}, nil }
	screen := "────────────────────────────────────────\n  list the files\n\n• There is 1\n\n" +
		"────────────────────────────────────────\nkiro_default · auto · ◔ 1%   /work\n\n" +
		"›  Kiro is working · 2s · Type to steer · Ctrl+S to queue\n"
	s.captureScrollback = func(context.Context, string, int) (string, error) { return screen, nil }
	id := discoverKiro(t, s)
	sessionID := ""
	for key, session := range id {
		if session.Inject == protocol.InjectTmux {
			sessionID = key
		}
	}
	if sessionID == "" {
		t.Fatalf("no pane-bound session: %v", id)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := make(chan []protocol.Message, 16)
	done := make(chan error, 1)
	go func() { done <- s.Follow(ctx, sessionID, out) }()

	want := parser.KiroTextID("p1", 1)
	next := func() protocol.Message {
		t.Helper()
		select {
		case batch := <-out:
			return batch[len(batch)-1]
		case <-ctx.Done():
			t.Fatal("follow sent nothing")
		}
		return protocol.Message{}
	}
	preview := next()
	if preview.ID != want || preview.Role != protocol.RoleAssistant || preview.Text != "There is 1" {
		t.Fatalf("preview = %+v, want %q under %q", preview, "There is 1", want)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(kiroLineReply + "\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	screen = strings.Replace(screen, "Kiro is working · 2s · Type to steer · Ctrl+S to queue", "ask a question or describe a task ↵", 1)
	final := next()
	if final.ID != want || final.Text != "There is 1 file." {
		t.Fatalf("finished reply = %+v, want it under the preview's id", final)
	}
	cancel()
	<-done
}

// A preview is withdrawn when the message turns out to have been a call: the
// record lands without the preview's id, and an empty text under that id
// takes it off the feed.
func TestKiroStreamPreviewIsWithdrawnWhenNoReplyClaimsIt(t *testing.T) {
	var preview kiroStreamPreview
	reading := func(text string) func() (string, string, int64, bool) {
		return func() (string, string, int64, bool) { return text, "p1/t1", 10, text != "" }
	}
	batch := preview.update(nil, "kiro:s", reading("Let me look"))
	if len(batch) != 1 || batch[0].Text != "Let me look" {
		t.Fatalf("first read = %+v", batch)
	}
	if batch := preview.update(nil, "kiro:s", reading("Let me look")); len(batch) != 0 {
		t.Errorf("an unchanged preview was sent again: %+v", batch)
	}
	call := []protocol.Message{{ID: "tooluse_1", Role: protocol.RoleTool}}
	batch = preview.update(call, "kiro:s", reading(""))
	if len(batch) != 2 || batch[1].ID != "p1/t1" || batch[1].Text != "" || batch[1].Role != protocol.RoleAssistant {
		t.Fatalf("withdrawal = %+v", batch)
	}
	if preview.id != "" {
		t.Errorf("preview still held after withdrawal: %+v", preview)
	}
}
