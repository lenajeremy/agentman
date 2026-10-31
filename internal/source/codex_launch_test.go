package source

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

type launchResult struct {
	thread string
	err    error
}

func startThreadAsync(src *CodexSource, dir, prompt string) <-chan launchResult {
	result := make(chan launchResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		thread, err := src.StartThread(ctx, dir, prompt)
		result <- launchResult{thread, err}
	}()
	return result
}

// playLaunch is the daemon's side of a launch: the thread is started, and
// its rollout is written when the first message goes in.
func playLaunch(t *testing.T, conn *fakeCodexConn, rollout string) turnStart {
	t.Helper()
	start := conn.expect(t, "thread/start")
	var params map[string]any
	json.Unmarshal(start.Params, &params)
	if params["cwd"] != "/work/api" {
		t.Fatalf("thread/start = %v", params)
	}
	conn.reply(t, start, map[string]any{"thread": map[string]any{"id": "t9", "cwd": "/work/api", "path": rollout, "status": map[string]any{"type": "idle"}}})
	request := conn.expect(t, "turn/start")
	var turn turnStart
	json.Unmarshal(request.Params, &turn)
	if err := os.WriteFile(rollout, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	conn.reply(t, request, map[string]any{"turn": map[string]any{"id": "u1", "status": "inProgress"}})
	return turn
}

func shortLaunchHold(t *testing.T) {
	t.Helper()
	previous := codexLaunchHold
	codexLaunchHold = 100 * time.Millisecond
	t.Cleanup(func() { codexLaunchHold = previous })
}

func TestCodexStartsALaunchOnTheDaemonAndLetsItGo(t *testing.T) {
	shortLaunchHold(t)
	home := t.TempDir()
	src, conn := connectedDaemonSource(t, home, nil)
	rollout := filepath.Join(home, "rollout-t9.jsonl")

	result := startThreadAsync(src, "/work/api", "add a health check")
	turn := playLaunch(t, conn, rollout)
	if turn.ThreadID != "t9" || len(turn.Input) != 1 || turn.Input[0]["text"] != "add a health check" {
		t.Fatalf("first message = %+v", turn)
	}
	launched := <-result
	if launched.err != nil || launched.thread != "t9" {
		t.Fatalf("StartThread = %+v", launched)
	}
	// Starting it joined Agentman; once the terminal has it, Agentman lets
	// go, so closing the terminal closes the session.
	conn.expect(t, "thread/unsubscribe")
}

// Watched from the phone by then, it stays joined until the phone leaves.
func TestCodexKeepsALaunchJoinedWhileItIsWatched(t *testing.T) {
	shortLaunchHold(t)
	home := t.TempDir()
	src, conn := connectedDaemonSource(t, home, nil)
	result := startThreadAsync(src, "/work/api", "add a health check")
	playLaunch(t, conn, filepath.Join(home, "rollout-t9.jsonl"))
	if launched := <-result; launched.err != nil {
		t.Fatal(launched.err)
	}
	follow, err := src.streams.open(context.Background(), "t9")
	if err != nil {
		t.Fatal(err)
	}
	conn.quiet(t) // already joined: no second join, and the hold ran out without leaving
	time.Sleep(2 * codexLaunchHold)
	conn.quiet(t)
	go src.streams.close(follow)
	conn.reply(t, conn.expect(t, "thread/unsubscribe"), map[string]any{"status": "unsubscribed"})
}

func TestCodexLaunchesTheOldWayWhenTheDaemonCannot(t *testing.T) {
	home := t.TempDir()
	alone, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alone.StartThread(context.Background(), "/work/api", "hi"); !errors.Is(err, ErrNoCodexDaemon) {
		t.Fatalf("not connected: %v", err)
	}
	src, conn := connectedDaemonSource(t, home, nil)
	for _, prompt := range []string{"", "  ", "/init", "!ls"} {
		if _, err := src.StartThread(context.Background(), "/work/api", prompt); !errors.Is(err, ErrNoCodexDaemon) {
			t.Errorf("prompt %q: %v, want the old launch", prompt, err)
		}
	}
	conn.quiet(t)
}

// Two launched in one folder: each pane names its thread, so each session is
// its own, which matching by folder could never tell apart.
func TestCodexTwoLaunchesInOneFolderAreEachTheirOwn(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	one := writeDaemonRollout(t, home, "01a19300-0000-7000-8000-000000000001", "/work/api", now.Add(-time.Minute), now.Add(-time.Second), "task_started")
	two := writeDaemonRollout(t, home, "01a19300-0000-7000-8000-000000000002", "/work/api", now.Add(-time.Minute), now.Add(-2*time.Second), "task_complete")
	src := daemonSource(t, home, true,
		openThread("01a19300-0000-7000-8000-000000000001", one, "active"),
		openThread("01a19300-0000-7000-8000-000000000002", two, "idle"))
	pane := func(thread string) tmux.Session {
		name, _ := src.ResumedSession(thread, "agentman-codex-1793400000000-ab12")
		return tmux.Session{Name: name, Command: "codex", Cwd: "/work/api", PanePID: 1, Created: now.Add(-time.Minute)}
	}
	first, second := pane("01a19300-0000-7000-8000-000000000001"), pane("01a19300-0000-7000-8000-000000000002")
	src.listPanes = func(context.Context) ([]tmux.Session, error) { return []tmux.Session{second, first}, nil }

	got := discovered(t, src)
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	for paneName, wantState := range map[string]protocol.State{first.Name: protocol.StateBusy, second.Name: protocol.StateIdle} {
		session, ok := got["codex:tmux-"+paneName]
		if !ok || session.State != wantState || session.Inject != protocol.InjectTmux {
			t.Errorf("%s: %+v", paneName, session)
		}
	}
	src.mu.RLock()
	defer src.mu.RUnlock()
	if src.sessions["codex:tmux-"+first.Name].transcript != one || src.sessions["codex:tmux-"+second.Name].transcript != two {
		t.Fatal("a pane reads the other launch's conversation")
	}
}
