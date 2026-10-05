package source

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// codexWithPanes builds a Codex source over a fake home and fake panes. The
// question check is faked too: the real one captures, and can press a key
// in, whatever tmux server the test runs beside.
func codexWithPanes(t *testing.T, home string, panes *[]tmux.Session) *CodexSource {
	t.Helper()
	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	src.processCheck = alwaysRunning
	src.listPanes = func(context.Context) ([]tmux.Session, error) { return *panes, nil }
	src.revealQuestion = func(context.Context, string) (string, error) { return "", nil }
	return src
}

func codexRows(t *testing.T, src *CodexSource) map[string]protocol.Session {
	t.Helper()
	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]protocol.Session{}
	for _, session := range sessions {
		if _, twice := rows[session.ID]; twice {
			t.Errorf("%s is listed twice", session.ID)
		}
		rows[session.ID] = session
	}
	return rows
}

// Codex records no pid, so its rollouts are matched to panes by directory, and
// a directory with two panes cannot be matched safely. Those panes used to be
// dropped altogether: with 14 panes in one directory, 7 in another and 1 in a
// third, the phone showed one session. Each pane must still be reachable —
// to send to, stop or end — even when its conversation cannot be shown.
func TestEveryCodexPaneHasARowEvenWhenTwoShareADirectory(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	writeCodexRollout(t, home, "01a0d347-e236-7f42-8d47-9529f1bf0001", "", "/work/a",
		now.Add(-10*time.Minute), now.Add(-time.Minute), "task_complete")

	var panes []tmux.Session
	for directory, count := range map[string]int{"/work/a": 14, "/work/b": 7, "/work/c": 1} {
		for i := range count {
			panes = append(panes, tmux.Session{
				Name: fmt.Sprintf("agentman-codex-%s-%02d", directory[len(directory)-1:], i),
				Cwd:  directory, Command: "codex", PanePID: 1000 + len(panes),
				Created: now.Add(-time.Hour),
			})
		}
	}
	rows := codexRows(t, codexWithPanes(t, home, &panes))

	for _, pane := range panes {
		row, ok := rows["codex:"+tmuxID(pane.Name)]
		if !ok {
			t.Errorf("pane %s has no row", pane.Name)
			continue
		}
		if row.Inject != protocol.InjectTmux || row.AgentPID != pane.PanePID {
			t.Errorf("pane %s: inject %s, pid %d", pane.Name, row.Inject, row.AgentPID)
		}
	}
}

// A pane that had its conversation to itself keeps it when a second Codex
// opens in the same directory. Which rollout belongs to which pane was settled
// while there was only one; a newcomer does not make that uncertain.
func TestACodexPaneKeepsItsConversationWhenASecondOpensBesideIt(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	thread := "01a0d347-e236-7f42-8d47-9529f1bf0002"
	writeCodexRollout(t, home, thread, "", "/work/a", now.Add(-10*time.Minute), now.Add(-time.Minute), "task_complete")

	first := tmux.Session{Name: "agentman-codex-first", Cwd: "/work/a", Command: "codex", PanePID: 100, Created: now.Add(-time.Hour)}
	panes := []tmux.Session{first}
	src := codexWithPanes(t, home, &panes)
	if rows := codexRows(t, src); rows["codex:"+tmuxID(first.Name)].NativeID != thread {
		t.Fatalf("the only pane did not claim its rollout: %+v", rows)
	}

	panes = append(panes, tmux.Session{Name: "agentman-codex-second", Cwd: "/work/a", Command: "codex", PanePID: 200, Created: now})
	rows := codexRows(t, src)
	if got := rows["codex:"+tmuxID(first.Name)]; got.NativeID != thread || got.Inject != protocol.InjectTmux {
		t.Errorf("the first pane lost its conversation: %+v", got)
	}
	if got, ok := rows["codex:"+tmuxID("agentman-codex-second")]; !ok || got.Inject != protocol.InjectTmux {
		t.Errorf("the second pane has no reachable row: %+v", got)
	}
	if _, listedTwice := rows["codex:"+thread]; listedTwice {
		t.Error("the conversation is listed again beside the pane that has it")
	}
}
