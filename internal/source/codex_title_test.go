package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

func TestCodexTitleName(t *testing.T) {
	for title, want := range map[string]string{
		"Respond to greeting | intro-to-hacking": "Respond to greeting",
		"⠋ Fix the parser | app":                 "Fix the parser",
		"Compare a | b output | app":             "Compare a | b output",
		"intro-to-hacking":                       "",
		"":                                       "",
		"Jeremiahs-MacBook.local":                "",
	} {
		if got := codexTitleName(title); got != want {
			t.Errorf("codexTitleName(%q) = %q, want %q", title, got, want)
		}
	}
}

// writeCodexNames writes session_index.jsonl as Codex keeps it: a line a
// naming, the last for a thread its name.
func writeCodexNames(t *testing.T, home string, names ...[2]string) {
	t.Helper()
	lines := make([]any, 0, len(names))
	for _, name := range names {
		lines = append(lines, obj{"id": name[0], "thread_name": name[1], "updated_at": "2026-10-27T15:01:11Z"})
	}
	writeJSONL(t, filepath.Join(home, ".codex", "session_index.jsonl"), lines)
}

func codexTranscript(src *CodexSource, id string) string {
	src.mu.RLock()
	defer src.mu.RUnlock()
	return src.sessions[id].transcript
}

// Codex 0.164 goes on with a resumed conversation in the rollout it started
// in, older than the pane resuming it. The pane sat empty beside its own
// conversation, which showed as one nobody could type to: "start the
// session with am codex". The pane's title names the conversation.
func TestACodexPaneShowsTheConversationItsTitleNames(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	resumed := "01a17de0-f7c0-7ab1-99dc-d6640193c578"
	other := "01a17de0-f7c0-7ab1-99dc-d6640193c579"
	writeCodexRollout(t, home, resumed, "", "/work/app", now.Add(-3*time.Hour), now.Add(-time.Minute), "task_complete")
	// Newer, and started after the pane: the timing rule's pick.
	writeCodexRollout(t, home, other, "", "/work/app", now.Add(-50*time.Second), now.Add(-30*time.Second), "task_complete")
	writeCodexNames(t, home, [2]string{resumed, "Respond to greeting"}, [2]string{other, "Something else"})

	name := "agentman-codex-1793122628943-102e89637901d639"
	panes := []tmux.Session{{Name: name, Cwd: "/work/app", Command: "codex", PanePID: 42,
		Created: now.Add(-time.Minute), Title: "Respond to greeting | app"}}
	src := codexWithPanes(t, home, &panes)
	rows := codexRows(t, src)
	id := "codex:" + tmuxID(name)
	if row := rows[id]; row.NativeID != resumed || row.Inject != protocol.InjectTmux {
		t.Fatalf("the pane's row is %+v", row)
	}
	if transcript := codexTranscript(src, id); !strings.Contains(transcript, resumed) {
		t.Errorf("the pane reads %q", transcript)
	}
	if _, ok := rows["codex:"+resumed]; ok {
		t.Errorf("the conversation is listed again on its own: %+v", rows)
	}
	if row := rows["codex:"+other]; row.Inject != protocol.InjectNone {
		t.Errorf("the other conversation took a pane: %+v", row)
	}

	// Started afresh in the pane, a conversation has no name for a while:
	// the timing rule settles it as before.
	panes[0].Title = "app"
	if row := codexRows(t, src)[id]; row.NativeID != other {
		t.Errorf("untitled, the pane shows %q", row.NativeID)
	}
}

// Of several conversations of one name, the pane's folder's, and of those
// the one written last.
func TestACodexTitleNamingSeveralConversationsMeansTheFoldersNewest(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	older := "01a0edc1-5467-78b1-8fe5-d32dc8283211"
	elsewhere := "01a0eefa-18bb-7d20-b05c-bde7cac1ed21"
	newest := "01a17de0-f7c0-7ab1-99dc-d6640193c578"
	writeCodexRollout(t, home, older, "", "/work/app", now.Add(-5*time.Hour), now.Add(-4*time.Hour), "task_complete")
	writeCodexRollout(t, home, elsewhere, "", "/work/site", now.Add(-2*time.Hour), now.Add(-10*time.Second), "task_complete")
	writeCodexRollout(t, home, newest, "", "/work/app", now.Add(-3*time.Hour), now.Add(-time.Minute), "task_complete")
	writeCodexNames(t, home, [2]string{older, "Respond to greeting"}, [2]string{elsewhere, "Respond to greeting"},
		[2]string{newest, "Draft"}, [2]string{newest, "Respond to greeting"})

	name := "agentman-codex-1793122628943-102e89637901d640"
	panes := []tmux.Session{{Name: name, Cwd: "/work/app", Command: "codex", PanePID: 42,
		Created: now.Add(-time.Minute), Title: "Respond to greeting | app"}}
	src := codexWithPanes(t, home, &panes)
	if row := codexRows(t, src)["codex:"+tmuxID(name)]; row.NativeID != newest {
		t.Fatalf("the pane shows %q, want %q", row.NativeID, newest)
	}
}

// A daemon started after the conversation's last turn finds it too, past the
// window a sweep reads rollouts in, and it shows the pane's history rather
// than nothing.
func TestACodexTitleFindsAQuietConversationAfterARestart(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	thread := "01a17c62-8fd2-7083-9f01-86cf21d2edb5"
	// Its day is past the days a sweep reads, as a conversation resumed
	// weeks on is.
	path := writeCodexRollout(t, home, thread, "", "/work/agentman", now.AddDate(0, 0, -40), now.Add(-8*time.Hour), "task_complete")
	writeCodexNames(t, home, [2]string{thread, "Await further instructions"})

	name := "agentman-codex-1793088148220-9b6a0c944f7dd759"
	panes := []tmux.Session{{Name: name, Cwd: "/work/agentman", Command: "codex", PanePID: 42,
		Created: now.Add(-10 * time.Hour), Title: "Await further instructions | agentman"}}
	src := codexWithPanes(t, home, &panes)
	id := "codex:" + tmuxID(name)
	if row := codexRows(t, src)[id]; row.NativeID != thread {
		t.Fatalf("the pane's row is %+v", row)
	}
	if transcript := codexTranscript(src, id); transcript != path {
		t.Errorf("the pane reads %q", transcript)
	}

	// Another sweep with nothing changed reads nothing it read before.
	if err := os.Remove(filepath.Join(home, ".codex", "session_index.jsonl")); err != nil {
		t.Fatal(err)
	}
	if row := codexRows(t, src)[id]; row.NativeID == thread {
		t.Errorf("the pane still shows a conversation no name in the index names")
	}
}

// A title from another folder's conversation, or a name the index lacks,
// claims nothing.
func TestACodexTitleClaimsNothingElsewhere(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	thread := "01a17de0-f7c0-7ab1-99dc-d6640193c580"
	writeCodexRollout(t, home, thread, "", "/work/site", now.Add(-3*time.Hour), now.Add(-time.Minute), "task_complete")
	writeCodexNames(t, home, [2]string{thread, "Respond to greeting"})

	panes := []tmux.Session{
		{Name: "agentman-codex-1-a", Cwd: "/work/app", Command: "codex", PanePID: 42, Created: now.Add(-time.Minute), Title: "Respond to greeting | app"},
		{Name: "agentman-codex-1-b", Cwd: "/work/site", Command: "codex", PanePID: 43, Created: now.Add(-time.Minute), Title: "Unknown | site"},
	}
	src := codexWithPanes(t, home, &panes)
	rows := codexRows(t, src)
	for _, pane := range panes {
		if row := rows["codex:"+tmuxID(pane.Name)]; row.NativeID != pane.Name {
			t.Errorf("%s shows %q", pane.Name, row.NativeID)
		}
	}
	if row := rows["codex:"+thread]; row.Inject != protocol.InjectNone {
		t.Errorf("the conversation is %+v", row)
	}
}
