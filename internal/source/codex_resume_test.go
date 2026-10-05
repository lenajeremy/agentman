package source

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// `codex resume <id>` keeps writing the thread's original rollout, whose
// session_meta is as old as the conversation. A pane may only claim a
// rollout started after it, so the reopened pane and its conversation came
// back as two rows: the pane, which the phone was sent to, with an empty
// transcript forever, and the conversation, read-only, beside it. The pane is
// now named for the thread it resumes, and claims that thread's rollout.
func TestAResumedCodexThreadIsShownInThePaneThatResumedIt(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	thread := "01a0d347-e236-7f42-8d47-9529f1bf8658"
	writeCodexRollout(t, home, thread, "", "/work/app", now.Add(-50*time.Hour), now.Add(-time.Minute), "task_complete")

	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	pane, id := src.ResumedSession(thread, "agentman-codex-1791190000000-abcdef0123456789")
	if !strings.HasPrefix(pane, tmux.Prefix) || id != "codex:"+tmuxID(pane) {
		t.Fatalf("named the resume pane %q and session %q", pane, id)
	}

	panes := []tmux.Session{{Name: pane, Cwd: "/work/app", Command: "codex", PanePID: 42, Created: now.Add(-2 * time.Minute)}}
	src = codexWithPanes(t, home, &panes)
	rows := codexRows(t, src)
	if len(rows) != 1 {
		t.Fatalf("one resumed conversation became %d rows: %+v", len(rows), rows)
	}
	row, ok := rows[id]
	if !ok || row.NativeID != thread || row.Inject != protocol.InjectTmux {
		t.Fatalf("the resumed pane's row is %+v", row)
	}
	src.mu.RLock()
	transcript := src.sessions[id].transcript
	src.mu.RUnlock()
	if !strings.Contains(transcript, thread) {
		t.Errorf("the resumed pane reads %q, not the thread's rollout", transcript)
	}
}

// Reopened from folder history before its first new turn, the rollout is
// still old enough to be outside the live window. History has already found
// it, so the pane can show it straight away.
func TestAResumedCodexThreadIsShownBeforeItsFirstNewTurn(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	thread := "01a0d347-e236-7f42-8d47-9529f1bf8659"
	path := writeCodexRollout(t, home, thread, "", "/work/app", now.Add(-50*time.Hour), now.Add(-49*time.Hour), "task_complete")

	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	src.past.remember("codex:"+thread, path) // as a folder listing does
	pane, id := src.ResumedSession(thread, "agentman-codex-1791190000000-abcdef0123456789")
	panes := []tmux.Session{{Name: pane, Cwd: "/work/app", Command: "codex", PanePID: 42, Created: now}}
	src.listPanes = func(context.Context) ([]tmux.Session, error) { return panes, nil }
	src.processCheck = alwaysRunning
	src.revealQuestion = func(context.Context, string) (string, error) { return "", nil }

	rows := codexRows(t, src)
	if row, ok := rows[id]; !ok || row.NativeID != thread {
		t.Fatalf("the resumed pane's row is %+v (rows %+v)", row, rows)
	}
}

// A name the daemon could not use is not offered: the daemon's own naming
// stands.
func TestCodexLeavesAResumeOfAnOddIDToTheDaemon(t *testing.T) {
	src, err := NewCodexSource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, native := range []string{"", "a/b", "x y", strings.Repeat("a", 300)} {
		if pane, id := src.ResumedSession(native, "agentman-codex-1-a"); pane != "" || id != "" {
			t.Errorf("ResumedSession(%q) = %q, %q", native, pane, id)
		}
	}
}

// Another conversation in the same folder that is newer must not take the
// pane that was opened to resume this one.
func TestANewerConversationDoesNotTakeAResumedPane(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	thread := "01a0d347-e236-7f42-8d47-9529f1bf8660"
	writeCodexRollout(t, home, thread, "", "/work/app", now.Add(-50*time.Hour), now.Add(-2*time.Minute), "task_complete")
	writeCodexRollout(t, home, "01a0d347-e236-7f42-8d47-9529f1bf8661", "", "/work/app",
		now.Add(-30*time.Second), now.Add(-10*time.Second), "task_complete")

	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	pane, id := src.ResumedSession(thread, "agentman-codex-1791190000000-abcdef0123456789")
	panes := []tmux.Session{{Name: pane, Cwd: "/work/app", Command: "codex", PanePID: 42, Created: now.Add(-time.Minute)}}
	src = codexWithPanes(t, home, &panes)
	if row := codexRows(t, src)[id]; row.NativeID != thread {
		t.Fatalf("the resumed pane shows %q, not the thread it reopened", row.NativeID)
	}
}
