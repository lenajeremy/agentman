package source

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// connectedDaemonSource is a Codex adapter connected to a fake daemon that
// has the given threads open, discovered once.
func connectedDaemonSource(t *testing.T, home string, panes []tmux.Session, threads ...codexDaemonThread) (*CodexSource, *fakeCodexConn) {
	t.Helper()
	fake := startFakeCodexDaemon(t)
	server := newTestCodexAppServer(t, fake.socket)
	src := daemonSource(t, home, true, threads...)
	src.appServer = server
	src.streams = newCodexStreams(server)
	src.requests = newCodexRequests()
	server.handle = func(msg codexRPCMessage) {
		src.live.apply(msg)
		src.streams.dispatch(msg)
		src.requests.apply(msg)
	}
	runCodexAppServer(t, server)
	conn := fake.accept(t)
	conn.handshake(t)
	waitConnected(t, server)

	if panes != nil {
		src.listPanes = func(context.Context) ([]tmux.Session, error) { return panes, nil }
	}
	if _, err := src.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	return src, conn
}

type sendResult struct {
	mode protocol.InjectMode
	err  error
}

func injectAsync(src *CodexSource, id, text string, images []string) <-chan sendResult {
	result := make(chan sendResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		mode, err := src.InjectWithAttachments(ctx, id, text, images)
		result <- sendResult{mode, err}
	}()
	return result
}

func awaitSend(t *testing.T, result <-chan sendResult) sendResult {
	t.Helper()
	select {
	case r := <-result:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("the send never returned")
		return sendResult{}
	}
}

type turnStart struct {
	ThreadID string           `json:"threadId"`
	Input    []map[string]any `json:"input"`
}

func expectTurnStart(t *testing.T, conn *fakeCodexConn) turnStart {
	t.Helper()
	request := conn.expect(t, "turn/start")
	var params turnStart
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatal(err)
	}
	conn.reply(t, request, map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}})
	return params
}

// A Codex started in the user's own terminal app has no pane, and was
// read-only. Through the daemon it takes messages.
func TestCodexMessagesASessionWithNoPaneThroughTheDaemon(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeDaemonRollout(t, home, "t1", "/work/api", now.Add(-time.Hour), now.Add(-time.Minute), "task_complete")
	src, conn := connectedDaemonSource(t, home, nil, openThread("t1", path, "idle"))

	src.mu.RLock()
	advertised := src.sessions["codex:t1"].meta.Inject
	src.mu.RUnlock()
	if advertised != protocol.InjectAPI {
		t.Fatalf("advertised %s, want api: it can be messaged now", advertised)
	}

	result := injectAsync(src, "codex:t1", "add a test for it", nil)
	params := expectTurnStart(t, conn)
	if params.ThreadID != "t1" || len(params.Input) != 1 || params.Input[0]["type"] != "text" || params.Input[0]["text"] != "add a test for it" {
		t.Fatalf("turn/start = %+v", params)
	}
	if r := awaitSend(t, result); r.err != nil || r.mode != protocol.InjectAPI {
		t.Fatalf("send = %+v", r)
	}
}

func TestCodexSendsPicturesAheadOfTheWords(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeDaemonRollout(t, home, "t1", "/work/api", now.Add(-time.Hour), now.Add(-time.Minute), "task_complete")
	src, conn := connectedDaemonSource(t, home, nil, openThread("t1", path, "idle"))

	result := injectAsync(src, "codex:t1", "what is wrong here?", []string{"/Users/me/.agentman/files/shot.png"})
	params := expectTurnStart(t, conn)
	if len(params.Input) != 2 || params.Input[0]["type"] != "localImage" || params.Input[0]["path"] != "/Users/me/.agentman/files/shot.png" ||
		params.Input[1]["text"] != "what is wrong here?" {
		t.Fatalf("input = %+v", params.Input)
	}
	if r := awaitSend(t, result); r.err != nil {
		t.Fatal(r.err)
	}
}

// Sent while Codex works, the message joins the turn and shows as waiting
// until the rollout records it.
func TestCodexShowsAMessageSentMidTurnAsWaiting(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeDaemonRollout(t, home, "t1", "/work/api", now.Add(-time.Hour), now.Add(-time.Second), "task_started")
	src, conn := connectedDaemonSource(t, home, nil, openThread("t1", path, "active"))

	result := injectAsync(src, "codex:t1", "also update the docs", nil)
	expectTurnStart(t, conn)
	if r := awaitSend(t, result); r.err != nil {
		t.Fatal(r.err)
	}
	found := discovered(t, src)
	if queued := found["codex:t1"].Queued; !slices.Equal(queued, []string{"also update the docs"}) {
		t.Fatalf("queued = %v", queued)
	}
	appendJSONL(t, path, obj{"timestamp": now.Format(time.RFC3339Nano), "type": "event_msg", "payload": obj{
		"type": "item_completed", "item": obj{"type": "UserMessage", "id": "u2",
			"content": []any{obj{"type": "text", "text": "also update the docs"}}}}})
	if queued := discovered(t, src)["codex:t1"].Queued; len(queued) != 0 {
		t.Fatalf("still queued after the rollout read it: %v", queued)
	}
}

func TestCodexTellsTheTerminalOnlyMessagesApart(t *testing.T) {
	for text, want := range map[string]bool{
		"/compact": true, "  /review the diff": true, "!ls -la": true, "use $openai-docs for this": true,
		"fix the login bug": false, "it costs $5": false, "see @src/main.go": false, "a/b path": false, "": false,
	} {
		if got := codexTerminalOnly(text); got != want {
			t.Errorf("codexTerminalOnly(%q) = %v, want %v", text, got, want)
		}
	}
}

// With no pane to type it into, a slash command is refused rather than sent
// to the model as words.
func TestCodexRefusesATerminalCommandWithNoTerminal(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeDaemonRollout(t, home, "t1", "/work/api", now.Add(-time.Hour), now.Add(-time.Minute), "task_complete")
	src, conn := connectedDaemonSource(t, home, nil, openThread("t1", path, "idle"))

	r := awaitSend(t, injectAsync(src, "codex:t1", "/review", nil))
	if !errors.Is(r.err, errCodexNeedsTerminal) {
		t.Fatalf("err = %v, want errCodexNeedsTerminal", r.err)
	}
	conn.quiet(t)
}

// A session in a pane sends through the daemon too, and still reports tmux
// delivery, which the apps key its slash commands and other controls on.
func TestCodexPaneSessionSendsThroughTheDaemonAndStaysTmux(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeDaemonRollout(t, home, "t1", "/work/api", now.Add(-time.Hour), now.Add(-time.Minute), "task_complete")
	pane := tmux.Session{Name: "agentman-codex-1758900000000-ab12", Command: "codex", Cwd: "/work/api", PanePID: 4242, Created: now.Add(-2 * time.Hour)}
	src, conn := connectedDaemonSource(t, home, []tmux.Session{pane}, openThread("t1", path, "idle"))

	id := "codex:tmux-" + pane.Name
	src.mu.RLock()
	advertised := src.sessions[id].meta.Inject
	src.mu.RUnlock()
	if advertised != protocol.InjectTmux {
		t.Fatalf("advertised %s, want tmux", advertised)
	}
	result := injectAsync(src, id, "run the tests", nil)
	if params := expectTurnStart(t, conn); params.ThreadID != "t1" {
		t.Fatalf("sent to %q", params.ThreadID)
	}
	if r := awaitSend(t, result); r.err != nil || r.mode != protocol.InjectTmux {
		t.Fatalf("send = %+v", r)
	}
}

func TestCodexStopsTheTurnUnderWay(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeDaemonRollout(t, home, "t1", "/work/api", now.Add(-time.Hour), now.Add(-time.Second), "task_started")
	src, conn := connectedDaemonSource(t, home, nil, openThread("t1", path, "active"))

	result := make(chan error, 1)
	go func() { result <- src.Interrupt(context.Background(), "codex:t1") }()
	list := conn.expect(t, "thread/turns/list")
	var params map[string]any
	json.Unmarshal(list.Params, &params)
	if params["threadId"] != "t1" || params["limit"] != float64(1) {
		t.Fatalf("turns/list params = %v", params)
	}
	conn.reply(t, list, map[string]any{"data": []any{map[string]any{"id": "turn-7", "status": "inProgress"}}})
	stop := conn.expect(t, "turn/interrupt")
	json.Unmarshal(stop.Params, &params)
	if params["threadId"] != "t1" || params["turnId"] != "turn-7" {
		t.Fatalf("interrupt params = %v", params)
	}
	conn.reply(t, stop, map[string]any{})
	if err := waitErr(t, result); err != nil {
		t.Fatal(err)
	}

	// Nothing running: nothing to stop, and no error.
	go func() { result <- src.Interrupt(context.Background(), "codex:t1") }()
	conn.reply(t, conn.expect(t, "thread/turns/list"), map[string]any{"data": []any{map[string]any{"id": "turn-7", "status": "interrupted"}}})
	if err := waitErr(t, result); err != nil {
		t.Fatal(err)
	}
	conn.quiet(t)
}

func TestCodexCannotStopASessionTheDaemonDoesNotHave(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	writeRolloutFrom(t, home, "x1", "/work/api", "codex_exec", "exec", now.Add(-time.Hour), now.Add(-time.Second), "task_started")
	src, conn := connectedDaemonSource(t, home, nil)
	if err := src.Interrupt(context.Background(), "codex:x1"); err == nil {
		t.Fatal("stopped a session the daemon does not have")
	}
	conn.quiet(t)
}
