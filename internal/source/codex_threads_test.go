package source

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// fakeCodexCalls answers the table's calls from canned replies.
type fakeCodexCalls struct {
	mu      sync.Mutex
	pages   [][]string                 // thread/loaded/list, one page per call
	threads map[string]codexWireThread // thread/read
	// readStarted, when set, is told each read and waits for release.
	readStarted chan string
	release     chan struct{}
	calls       []string
}

func (f *fakeCodexCalls) call(_ context.Context, method string, params, dest any) error {
	f.mu.Lock()
	f.calls = append(f.calls, method)
	f.mu.Unlock()
	raw, _ := json.Marshal(params)
	var p struct {
		Cursor   string `json:"cursor"`
		ThreadID string `json:"threadId"`
	}
	json.Unmarshal(raw, &p)
	var result any
	switch method {
	case "thread/loaded/list":
		page := 0
		if p.Cursor != "" {
			json.Unmarshal([]byte(p.Cursor), &page)
		}
		reply := map[string]any{"data": f.pages[page], "nextCursor": nil}
		if page+1 < len(f.pages) {
			next, _ := json.Marshal(page + 1)
			reply["nextCursor"] = string(next)
		}
		result = reply
	case "thread/read":
		if f.readStarted != nil {
			f.readStarted <- p.ThreadID
			<-f.release
		}
		f.mu.Lock()
		thread, ok := f.threads[p.ThreadID]
		f.mu.Unlock()
		if !ok {
			return &codexRPCError{Message: "thread not found"}
		}
		result = map[string]any{"thread": thread}
	default:
		return errors.New("unexpected call " + method)
	}
	data, _ := json.Marshal(result)
	return json.Unmarshal(data, dest)
}

func wireThread(id, status string, flags ...string) codexWireThread {
	path := "/h/.codex/sessions/rollout-" + id + ".jsonl"
	return codexWireThread{
		ID: id, Cwd: "/work/api", Path: &path, Model: "gpt-6.1-sol",
		Status: codexWireStatus{Type: status, Flags: flags}, CreatedAt: 100, UpdatedAt: 200,
	}
}

func notification(t *testing.T, method string, params any) codexRPCMessage {
	t.Helper()
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return codexRPCMessage{Method: method, Params: data}
}

func TestCodexLiveThreadsReadsEveryLoadedThreadOnConnect(t *testing.T) {
	fake := &fakeCodexCalls{
		pages: [][]string{{"a", "b"}, {"c"}},
		threads: map[string]codexWireThread{
			"a": wireThread("a", "idle"), "b": wireThread("b", "active"), "c": wireThread("c", "systemError"),
		},
	}
	live := newCodexLiveThreads(fake.call)
	if _, ready := live.snapshot(); ready {
		t.Fatal("ready before anything was read")
	}
	live.connected()
	threads, ready := live.snapshot()
	if !ready || len(threads) != 3 {
		t.Fatalf("ready=%v threads=%v, want all three pages read", ready, threads)
	}
	if threads["b"].state() != protocol.StateBusy || threads["c"].state() != protocol.StateIdle {
		t.Fatalf("states: b=%s c=%s", threads["b"].state(), threads["c"].state())
	}
	if threads["a"].Path != "/h/.codex/sessions/rollout-a.jsonl" || threads["a"].Model != "gpt-6.1-sol" {
		t.Fatalf("a = %+v", threads["a"])
	}
	live.lost()
	if _, ready := live.snapshot(); ready {
		t.Fatal("still trusted after the connection dropped")
	}
}

func TestCodexLiveThreadsFollowTheDaemonsNotifications(t *testing.T) {
	fake := &fakeCodexCalls{pages: [][]string{{"a"}}, threads: map[string]codexWireThread{"a": wireThread("a", "idle")}}
	live := newCodexLiveThreads(fake.call)
	live.connected()

	live.apply(notification(t, "thread/status/changed", map[string]any{
		"threadId": "a", "status": map[string]any{"type": "active", "activeFlags": []string{"waitingOnApproval"}}}))
	threads, _ := live.snapshot()
	if threads["a"].state() != protocol.StateWaitingInput {
		t.Fatalf("state = %s, want waiting on the approval", threads["a"].state())
	}

	live.apply(notification(t, "thread/settings/updated", map[string]any{
		"threadId": "a", "threadSettings": map[string]any{"model": "gpt-6.1-mini", "collaborationMode": map[string]any{"mode": "plan"}}}))
	threads, _ = live.snapshot()
	if threads["a"].Model != "gpt-6.1-mini" || threads["a"].Mode != "plan" {
		t.Fatalf("after settings: %+v", threads["a"])
	}

	started := wireThread("b", "idle")
	live.apply(notification(t, "thread/started", map[string]any{"thread": started}))
	ephemeral := wireThread("title", "active")
	ephemeral.Ephemeral = true
	live.apply(notification(t, "thread/started", map[string]any{"thread": ephemeral}))
	threads, _ = live.snapshot()
	if _, ok := threads["b"]; !ok || threads["title"].session() {
		t.Fatalf("after starts: %v", threads)
	}

	live.apply(notification(t, "thread/closed", map[string]any{"threadId": "a"}))
	live.apply(notification(t, "thread/status/changed", map[string]any{"threadId": "b", "status": map[string]any{"type": "notLoaded"}}))
	threads, _ = live.snapshot()
	if _, ok := threads["a"]; ok {
		t.Fatal("a closed thread is still listed")
	}
	if _, ok := threads["b"]; ok {
		t.Fatal("an unloaded thread is still listed")
	}
}

// A status change for a thread the table has not seen (opened again after it
// closed, say) is read to learn what it is.
func TestCodexLiveThreadsReadAThreadTheyDoNotKnow(t *testing.T) {
	fake := &fakeCodexCalls{pages: [][]string{{}}, threads: map[string]codexWireThread{"late": wireThread("late", "active")}}
	live := newCodexLiveThreads(fake.call)
	live.connected()
	live.apply(notification(t, "thread/status/changed", map[string]any{"threadId": "late", "status": map[string]any{"type": "active"}}))
	deadline := time.Now().Add(5 * time.Second)
	for {
		threads, _ := live.snapshot()
		if _, ok := threads["late"]; ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the unknown thread was never read")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A thread that closes while its read is under way must not come back when
// the read returns.
func TestCodexLiveThreadsDoNotBringBackAThreadThatClosedDuringItsRead(t *testing.T) {
	fake := &fakeCodexCalls{
		pages:       [][]string{{"a"}},
		threads:     map[string]codexWireThread{"a": wireThread("a", "idle")},
		readStarted: make(chan string, 1),
		release:     make(chan struct{}),
	}
	live := newCodexLiveThreads(fake.call)
	done := make(chan struct{})
	go func() {
		defer close(done)
		live.connected()
	}()
	<-fake.readStarted
	live.apply(notification(t, "thread/closed", map[string]any{"threadId": "a"}))
	close(fake.release)
	<-done
	threads, ready := live.snapshot()
	if !ready {
		t.Fatal("not ready after the read")
	}
	if _, ok := threads["a"]; ok {
		t.Fatal("a thread that closed during its read came back")
	}
}

// --- discovery with the daemon ---

// writeDaemonRollout writes a rollout as a terminal Codex on the shared
// daemon does: originator codex-tui, source vscode.
func writeDaemonRollout(t *testing.T, home, threadID, cwd string, startedAt, writtenAt time.Time, lastEvent string) string {
	return writeRolloutFrom(t, home, threadID, cwd, "codex-tui", "vscode", startedAt, writtenAt, lastEvent)
}

func writeRolloutFrom(t *testing.T, home, threadID, cwd, originator, source string, startedAt, writtenAt time.Time, lastEvent string) string {
	t.Helper()
	path := writeCodexRollout(t, home, threadID, threadID, cwd, startedAt, writtenAt, lastEvent)
	writeJSONL(t, path, []any{
		obj{"timestamp": startedAt.Format(time.RFC3339Nano), "type": "session_meta", "payload": obj{
			"id": threadID, "session_id": threadID, "cwd": cwd, "originator": originator, "source": source,
			"cli_version": "0.166.0", "timestamp": startedAt.Format(time.RFC3339Nano)}},
		obj{"timestamp": writtenAt.Format(time.RFC3339Nano), "type": "event_msg", "payload": obj{"type": lastEvent}},
	})
	touch(t, path, writtenAt)
	return path
}

// daemonSource is a Codex adapter whose daemon has the given threads open.
// ready false is a daemon still being read after a connect.
func daemonSource(t *testing.T, home string, ready bool, threads ...codexDaemonThread) *CodexSource {
	t.Helper()
	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	src.processCheck = alwaysRunning
	src.listPanes = noPanes
	live := newCodexLiveThreads(nil)
	live.ready = ready
	for _, thread := range threads {
		live.threads[thread.ID] = thread
	}
	src.live = live
	return src
}

func discovered(t *testing.T, src *CodexSource) map[string]protocol.Session {
	t.Helper()
	found, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]protocol.Session{}
	for _, session := range found {
		out[session.ID] = session
	}
	return out
}

func openThread(id, path, status string, flags ...string) codexDaemonThread {
	return codexDaemonThread{ID: id, Cwd: "/work/api", Path: path, Model: "gpt-6.1-sol", Status: status, Flags: flags}
}

// The ghost the rollout scan could not avoid: a terminal quit a minute ago
// left a recent rollout, and the daemon's own process passes the process
// probe, so the session stayed for half an hour. The daemon says it closed.
func TestCodexDaemonDropsAClosedTerminalSessionAtOnce(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	writeDaemonRollout(t, home, "closed", "/work/api", now.Add(-time.Hour), now.Add(-time.Minute), "task_complete")

	if got := discovered(t, daemonSource(t, home, true)); len(got) != 0 {
		t.Fatalf("got %v, want the closed session gone", got)
	}
	// Without the daemon, the rollout path still decides.
	if got := discovered(t, daemonSource(t, home, false)); len(got) != 1 {
		t.Fatalf("while the daemon is being read: got %d sessions, want the rollout's one", len(got))
	}
	src := daemonSource(t, home, false)
	src.live = nil
	if got := discovered(t, src); len(got) != 1 {
		t.Fatalf("with no daemon: got %d sessions, want the rollout's one", len(got))
	}
}

// The other half: a terminal left open and idle for hours is still open.
func TestCodexDaemonKeepsAnOpenSessionPastTheLiveWindow(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeDaemonRollout(t, home, "open", "/work/api", now.Add(-5*time.Hour), now.Add(-2*time.Hour), "task_complete")

	got := discovered(t, daemonSource(t, home, true, openThread("open", path, "idle")))
	session, ok := got["codex:open"]
	if !ok {
		t.Fatalf("got %v, want the open session though it has been idle for two hours", got)
	}
	if session.State != protocol.StateIdle {
		t.Fatalf("state = %s", session.State)
	}
}

func TestCodexDaemonDecidesTheState(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	// The rollout's tail says the turn is over; the daemon knows better.
	path := writeDaemonRollout(t, home, "t", "/work/api", now.Add(-time.Hour), now.Add(-time.Second), "task_complete")
	for _, c := range []struct {
		status string
		flags  []string
		want   protocol.State
	}{
		{"active", nil, protocol.StateBusy},
		{"active", []string{"waitingOnApproval"}, protocol.StateWaitingInput},
		{"active", []string{"waitingOnUserInput"}, protocol.StateWaitingInput},
		{"idle", nil, protocol.StateIdle},
		{"systemError", nil, protocol.StateIdle},
	} {
		got := discovered(t, daemonSource(t, home, true, openThread("t", path, c.status, c.flags...)))
		if state := got["codex:t"].State; state != c.want {
			t.Errorf("%s %v: state = %s, want %s", c.status, c.flags, state, c.want)
		}
	}
}

func TestCodexDaemonNamesTheModel(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeDaemonRollout(t, home, "t", "/work/api", now.Add(-time.Hour), now.Add(-time.Second), "task_complete")
	thread := openThread("t", path, "idle")
	thread.Model = "gpt-6.1-mini"
	if model := discovered(t, daemonSource(t, home, true, thread))["codex:t"].Model; model != "gpt-6.1-mini" {
		t.Fatalf("model = %q, want the daemon's", model)
	}
}

// codex exec, the ChatGPT app and terminals from before the daemon run
// servers of their own: the daemon not having them says nothing.
func TestCodexDaemonLeavesOtherCodexesToTheRollouts(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	writeRolloutFrom(t, home, "exec", "/work/a", "codex_exec", "exec", now.Add(-time.Hour), now.Add(-time.Minute), "task_started")
	writeRolloutFrom(t, home, "desktop", "/work/b", "Codex Desktop", "vscode", now.Add(-time.Hour), now.Add(-time.Minute), "task_complete")
	writeRolloutFrom(t, home, "oldtui", "/work/c", "codex-tui", "cli", now.Add(-time.Hour), now.Add(-time.Minute), "task_complete")

	got := discovered(t, daemonSource(t, home, true))
	for _, id := range []string{"codex:exec", "codex:desktop", "codex:oldtui"} {
		if _, ok := got[id]; !ok {
			t.Errorf("%s missing: %v", id, got)
		}
	}
	if got["codex:exec"].State != protocol.StateBusy {
		t.Errorf("exec state = %s, want the rollout's busy", got["codex:exec"].State)
	}
}

// Threads Codex runs for itself are never sessions, even if one has a rollout.
func TestCodexDaemonSkipsSubagentAndEphemeralThreads(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	sub := writeDaemonRollout(t, home, "sub", "/work/api", now.Add(-5*time.Hour), now.Add(-2*time.Hour), "task_complete")
	eph := writeDaemonRollout(t, home, "eph", "/work/api", now.Add(-5*time.Hour), now.Add(-2*time.Hour), "task_complete")
	child := openThread("sub", sub, "idle")
	child.ParentThreadID = "root"
	title := openThread("eph", eph, "idle")
	title.Ephemeral = true
	if got := discovered(t, daemonSource(t, home, true, child, title)); len(got) != 0 {
		t.Fatalf("got %v, want neither", got)
	}
}

// The process probe cannot see the daemon's sessions any better than the
// daemon can, so it no longer clears them.
func TestCodexDaemonSessionsDoNotDependOnTheProcessProbe(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeDaemonRollout(t, home, "t", "/work/api", now.Add(-time.Hour), now.Add(-time.Second), "task_started")
	src := daemonSource(t, home, true, openThread("t", path, "active"))
	src.processCheck = func(context.Context) bool { return false }
	if got := discovered(t, src); len(got) != 1 {
		t.Fatalf("got %d sessions, want the daemon's one", len(got))
	}
}

// A pane still running Codex vouches for its rollout even when the daemon
// does not have the thread: that Codex may be running a server of its own.
func TestCodexDaemonKeepsASessionItsPaneVouchesFor(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	writeDaemonRollout(t, home, "t", "/work/api", now.Add(-time.Hour), now.Add(-time.Minute), "task_complete")
	src := daemonSource(t, home, true)
	src.listPanes = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{
			Name: "agentman-codex-1758900000000-ab12", Command: "codex",
			Cwd: "/work/api", PanePID: 4242, Created: now.Add(-2 * time.Hour),
		}}, nil
	}
	got := discovered(t, src)
	if _, ok := got["codex:tmux-agentman-codex-1758900000000-ab12"]; !ok || len(got) != 1 {
		t.Fatalf("got %v, want the pane's session", got)
	}
}
