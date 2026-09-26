package source

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// writeCodexRollout places one rollout in the day directory of startedAt, the
// way Codex files them, and stamps its mtime to say when it was last written.
// The two are deliberately separate: a session started days ago and still being
// appended to is the case this file exists for.
func writeCodexRollout(
	t *testing.T, home, threadID, lineageID, cwd string,
	startedAt, writtenAt time.Time, lastEvent string,
) string {
	t.Helper()
	path := filepath.Join(home, ".codex", "sessions",
		startedAt.Format("2006"), startedAt.Format("01"), startedAt.Format("02"),
		"rollout-"+startedAt.Format("2006-01-02T15-04-05")+"-"+threadID+".jsonl")

	payload := obj{
		"id": threadID, "session_id": lineageID, "cwd": cwd,
		"cli_version": "0.156.1", "timestamp": startedAt.Format(time.RFC3339Nano),
	}
	writeJSONL(t, path, []any{
		obj{"timestamp": startedAt.Format(time.RFC3339Nano), "type": "session_meta", "payload": payload},
		obj{"timestamp": writtenAt.Format(time.RFC3339Nano), "type": "event_msg",
			"payload": obj{"type": lastEvent}},
	})
	if err := os.Chtimes(path, writtenAt, writtenAt); err != nil {
		t.Fatal(err)
	}
	return path
}

func discoverCodex(t *testing.T, home string) map[string]codexSession {
	t.Helper()
	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	src.processCheck = alwaysRunning
	src.listPanes = noPanes
	if _, err := src.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	src.mu.RLock()
	defer src.mu.RUnlock()
	out := map[string]codexSession{}
	for id, session := range src.sessions {
		out[id] = session
	}
	return out
}

// Regression: discovery scanned only today's and yesterday's directories, on
// the theory that "only today and yesterday can hold a live session". A rollout
// is filed under the date its session started and appended to for as long as
// that session lives, so a conversation open since last week was invisible.
func TestCodexFindsASessionStartedDaysAgo(t *testing.T) {
	home := t.TempDir()
	started := time.Now().AddDate(0, 0, -6)
	writeCodexRollout(t, home, "thread-old", "thread-old", "/work/api",
		started, time.Now().Add(-time.Minute), "task_started")

	sessions := discoverCodex(t, home)
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want the one started six days ago", len(sessions))
	}
	for _, session := range sessions {
		if session.meta.State != "busy" {
			t.Errorf("state = %s, want busy", session.meta.State)
		}
	}
}

// The bug as reported: a session working right now shown as idle. A finished
// rollout from a later day sat in a directory that was scanned, the live one
// from an earlier day did not, and the finished one claimed the pane.
func TestCodexPrefersTheLiveRolloutOverAFinishedSibling(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	// Still working, started days ago.
	writeCodexRollout(t, home, "thread-live", "thread-live", "/work/api",
		now.AddDate(0, 0, -3), now.Add(-30*time.Second), "task_started")
	// Finished, started yesterday.
	writeCodexRollout(t, home, "thread-done", "thread-done", "/work/api",
		now.AddDate(0, 0, -1), now.Add(-2*time.Minute), "task_complete")

	sessions := discoverCodex(t, home)
	live, ok := sessions["codex:thread-live"]
	if !ok {
		t.Fatalf("the live session was not discovered: %v", keysOf(sessions))
	}
	if live.meta.State != "busy" {
		t.Errorf("live session state = %s, want busy", live.meta.State)
	}
}

// Codex writes two identifiers: "id" for this rollout, "session_id" for the
// conversation it descends from. Reading the lineage made every thread in it
// report the same id, so two rollouts collided on one key and one silently
// replaced the other.
func TestCodexKeysAThreadOnItsOwnID(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	writeCodexRollout(t, home, "thread-parent", "thread-parent", "/work/api",
		now.AddDate(0, 0, -1), now.Add(-time.Minute), "task_complete")
	// A resumed or forked thread: its own id, its ancestor's session_id.
	writeCodexRollout(t, home, "thread-child", "thread-parent", "/work/api",
		now, now.Add(-30*time.Second), "task_started")

	sessions := discoverCodex(t, home)
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want both threads: %v", len(sessions), keysOf(sessions))
	}
	for id, session := range sessions {
		if !idNamesTranscript(id, session.transcript) {
			t.Errorf("session %s reads %s — the id does not name the transcript",
				id, filepath.Base(session.transcript))
		}
	}
}

// Older Codex wrote only session_id, and those rollouts must keep working.
func TestCodexFallsBackToSessionIDWhenThereIsNoThreadID(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeCodexRollout(t, home, "legacy", "legacy", "/work/api",
		now, now.Add(-time.Minute), "task_started")
	// Rewrite the header without an "id", as older versions produced.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.Replace(string(body), `"id":"`, `"unused":"`, 1)
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	sessions := discoverCodex(t, home)
	if _, ok := sessions["codex:legacy"]; !ok {
		t.Fatalf("a rollout with only session_id was dropped: %v", keysOf(sessions))
	}
}

func keysOf(m map[string]codexSession) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The id a session is keyed on must name the transcript it reads, or paging a
// session returns another thread's history.
func idNamesTranscript(id, transcript string) bool {
	return strings.Contains(filepath.Base(transcript), strings.TrimPrefix(id, "codex:"))
}

// Regression: the backward search for a turn boundary is bounded, and a long
// turn outruns it — a 78MB rollout on the machine this was found on had its
// task_started a megabyte behind the end. When the scan met its budget without
// finding a boundary it returned idle, so a session plainly working was
// reported as finished. An exhausted scan now keeps what was last known.
func TestCodexKeepsTheLastKnownStateWhenAScanFindsNothing(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeCodexRollout(t, home, "thread-long", "thread-long", "/work/api",
		now, now.Add(-time.Minute), "task_started")

	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	src.processCheck = alwaysRunning
	src.listPanes = noPanes

	// First sweep sees the boundary.
	answers := []protocol.State{"busy", ""}
	call := 0
	src.readActivity = func(context.Context, string, time.Time) (protocol.State, int64, error) {
		state := answers[min(call, len(answers)-1)]
		call++
		return state, time.Now().UnixMilli(), nil
	}
	if _, err := src.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, src, "codex:thread-long"); got != "busy" {
		t.Fatalf("first sweep state = %s, want busy", got)
	}

	// Second sweep's scan finds no boundary at all.
	touch(t, path, time.Now())
	if _, err := src.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, src, "codex:thread-long"); got != "busy" {
		t.Fatalf("state after an exhausted scan = %s, want the last known busy", got)
	}
}

// With nothing known yet, an exhausted scan still has to answer something, and
// idle is the safe answer — it is what the session looks like to a phone that
// has just connected.
func TestCodexFallsBackToIdleWhenNothingIsKnown(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	writeCodexRollout(t, home, "thread-new", "thread-new", "/work/api",
		now, now.Add(-time.Minute), "task_started")

	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	src.processCheck = alwaysRunning
	src.listPanes = noPanes
	src.readActivity = func(context.Context, string, time.Time) (protocol.State, int64, error) {
		return "", time.Now().UnixMilli(), nil
	}
	if _, err := src.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, src, "codex:thread-new"); got != protocol.StateIdle {
		t.Fatalf("state = %s, want idle when nothing has been observed", got)
	}
}

// Once state is known, a sweep reads forward over what was appended rather than
// searching back from the end again, so a boundary written since the last look
// is picked up however large the rollout has become.
func TestCodexPicksUpABoundaryAppendedSinceTheLastSweep(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeCodexRollout(t, home, "thread-tail", "thread-tail", "/work/api",
		now, now.Add(-time.Minute), "task_started")

	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	src.processCheck = alwaysRunning
	src.listPanes = noPanes
	if _, err := src.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, src, "codex:thread-tail"); got != "busy" {
		t.Fatalf("state = %s, want busy", got)
	}

	appendJSONL(t, path, obj{"timestamp": time.Now().Format(time.RFC3339Nano),
		"type": "event_msg", "payload": obj{"type": "task_complete"}})
	touch(t, path, time.Now())

	if _, err := src.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, src, "codex:thread-tail"); got != protocol.StateIdle {
		t.Fatalf("state = %s, want idle after task_complete was appended", got)
	}
}

func stateOf(t *testing.T, src *CodexSource, id string) protocol.State {
	t.Helper()
	src.mu.RLock()
	defer src.mu.RUnlock()
	session, ok := src.sessions[id]
	if !ok {
		t.Fatalf("session %s not discovered", id)
	}
	return session.meta.State
}

func touch(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func appendJSONL(t *testing.T, path string, record any) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(body, '\n')); err != nil {
		t.Fatal(err)
	}
}

// Regression: Follow captured the transcript once and tailed that path for the
// life of the subscription. Codex opens a new rollout when a conversation is
// resumed or forked and discovery rebinds the session to it — after which the
// old tail followed a file nobody writes to again and the phone simply stopped
// receiving anything, while the session still looked alive.
func TestCodexFollowSwitchesWhenTheTranscriptIsRebound(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	first := writeCodexRollout(t, home, "thread-first", "thread-first", "/work/api",
		now, now.Add(-time.Minute), "task_started")

	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	src.processCheck = alwaysRunning
	src.listPanes = noPanes
	if _, err := src.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out := make(chan []protocol.Message, 8)
	done := make(chan error, 1)
	go func() { done <- src.Follow(ctx, "codex:thread-first", out) }()
	awaitCodexAttached(t, first, out)

	// A resumed conversation: a new rollout, carrying the first one's lineage.
	second := writeCodexRollout(t, home, "thread-second", "thread-first", "/work/api",
		now.Add(time.Second), time.Now(), "task_started")
	appendJSONL(t, second, obj{"timestamp": time.Now().Format(time.RFC3339Nano),
		"type": "event_msg", "payload": obj{"type": "item_completed", "item": obj{
			"type": "AgentMessage", "id": "am-after-resume",
			"content": []any{obj{"type": "Text", "text": "carried on"}}}}})

	// Rebind the subscription's session to the new rollout, as discovery does.
	src.mu.Lock()
	session := src.sessions["codex:thread-first"]
	session.transcript = second
	src.sessions["codex:thread-first"] = session
	src.mu.Unlock()

	deadline := time.After(8 * time.Second)
	for {
		select {
		case batch := <-out:
			for _, message := range batch {
				if strings.Contains(message.Text, "carried on") {
					cancel()
					<-done
					return
				}
			}
		case <-deadline:
			cancel()
			<-done
			t.Fatal("nothing arrived from the rollout the session was rebound to")
		}
	}
}

// Regression: an npm-installed Codex was not merely reported idle, it was
// invisible.
//
// The npm package is a `#!/usr/bin/env node` script, so tmux reports `node` as
// the pane's command and `pgrep -x codex` matches nothing. Discovery skipped
// the pane for having the wrong command name, and the failing process probe
// then cleared every Codex session on the machine on every sweep.
func TestCodexSeesAnNpmInstallWhosePaneReportsNode(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	writeCodexRollout(t, home, "thread-npm", "thread-npm", "/work/api",
		now.Add(-time.Minute), now.Add(-10*time.Second), "task_started")

	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	// pgrep -x codex finds nothing: the process is named node.
	src.processCheck = func(context.Context) bool { return false }
	src.listPanes = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{
			Name:    "agentman-codex-1758900000000-ab12",
			Command: "node",
			Cwd:     "/work/api",
			PanePID: 4242,
			Created: now.Add(-2 * time.Minute),
		}}, nil
	}

	found, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("got %d sessions, want the one running under node", len(found))
	}
	if found[0].State != protocol.StateBusy {
		t.Errorf("state = %s, want busy", found[0].State)
	}
	if found[0].Inject != protocol.InjectTmux {
		t.Errorf("inject = %v, want tmux: nothing can be sent to the session otherwise", found[0].Inject)
	}
}

// A Codex pane is proof Codex is running even when the process probe disagrees,
// because that gate does not downgrade a session — it deletes it.
func TestCodexKeepsSessionsWhenTheProcessProbeCannotSeeTheInstall(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	writeCodexRollout(t, home, "thread-npm", "thread-npm", "/work/api",
		now.Add(-time.Minute), now.Add(-10*time.Second), "task_started")

	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	probed := false
	src.processCheck = func(context.Context) bool { probed = true; return false }
	src.listPanes = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{
			Name: "agentman-codex-1758900000000-ab12", Command: "node",
			Cwd: "/work/api", PanePID: 4242, Created: now.Add(-2 * time.Minute),
		}}, nil
	}
	if _, err := src.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}

	src.mu.RLock()
	tracked := len(src.sessions)
	src.mu.RUnlock()
	if tracked == 0 {
		t.Error("the session map was cleared while a Codex pane was open")
	}
	if probed {
		t.Error("an open Codex pane already settles liveness; pgrep should not have run")
	}
}

// With no pane to vouch for it, a negative probe still clears everything — the
// ghost sessions that check exists to remove.
func TestCodexClearsSessionsWhenNothingIsRunning(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	writeCodexRollout(t, home, "thread-gone", "thread-gone", "/work/api",
		now.Add(-time.Minute), now.Add(-10*time.Second), "task_started")

	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	src.processCheck = func(context.Context) bool { return false }
	src.listPanes = noPanes
	found, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("got %d sessions, want none: no codex process and no pane", len(found))
	}
}

// Regression: a rebind replayed the whole of the rollout it switched to.
//
// Codex copies the conversation it is resuming into the rollout it opens, so on
// a long one that replay is a transcript the app already holds — delivered a
// megabyte per tick, which on the 78MB rollout this was found on is twenty
// seconds of flooding. Past the budget the tail attaches at the end instead.
func TestCodexDoesNotReplayALongRolloutOnRebind(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	first := writeCodexRollout(t, home, "thread-first", "thread-first", "/work/api",
		now, now.Add(-time.Minute), "task_started")

	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	src.processCheck = alwaysRunning
	src.listPanes = noPanes
	if _, err := src.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan []protocol.Message, 64)
	done := make(chan error, 1)
	go func() { done <- src.Follow(ctx, "codex:thread-first", out) }()
	awaitCodexAttached(t, first, out)

	// A resumed conversation, carrying the history Codex copied into it, and
	// long enough to be past the replay budget.
	second := writeCodexRollout(t, home, "thread-second", "thread-first", "/work/api",
		now.Add(time.Second), time.Now(), "task_started")
	appendJSONL(t, second, codexAgentMessage("am-replayed", "carried over from before"))
	appendCodexPadding(t, second, codexRebindReplayBytes+512*1024)

	src.mu.Lock()
	session := src.sessions["codex:thread-first"]
	session.transcript = second
	src.sessions["codex:thread-first"] = session
	src.mu.Unlock()

	// Written repeatedly so the test does not depend on landing after the tick
	// that swapped the tail: whenever that happens, a later append follows it.
	go func() {
		for n := 0; ctx.Err() == nil; n++ {
			appendJSONL(t, second, codexAgentMessage(
				fmt.Sprintf("am-live-%d", n), "written after the rebind"))
			time.Sleep(150 * time.Millisecond)
		}
	}()

	deadline := time.After(20 * time.Second)
	for {
		select {
		case batch := <-out:
			for _, message := range batch {
				if strings.Contains(message.Text, "carried over from before") {
					cancel()
					<-done
					t.Fatal("the rebind replayed history the app already had")
				}
				if strings.Contains(message.Text, "written after the rebind") {
					cancel()
					<-done
					return
				}
			}
		case <-deadline:
			cancel()
			<-done
			t.Fatal("nothing arrived from the rollout the session was rebound to")
		}
	}
}

// awaitCodexAttached blocks until a live subscription is reading path, by
// appending to it until something it appended comes back.
//
// Starting a Follow and rebinding it straight away is a race the test would
// lose silently: Follow may read the session only after the rebind, attach
// directly to the new transcript, and seek past everything the test wrote.
func awaitCodexAttached(t *testing.T, path string, out <-chan []protocol.Message) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for n := 0; ; n++ {
		appendJSONL(t, path, codexAgentMessage(
			fmt.Sprintf("am-attach-%d", n), "attach probe"))
		select {
		case batch := <-out:
			for _, message := range batch {
				if strings.Contains(message.Text, "attach probe") {
					return
				}
			}
		case <-time.After(200 * time.Millisecond):
		case <-deadline:
			t.Fatalf("the subscription never attached to %s", filepath.Base(path))
		}
	}
}

// codexAgentMessage is one rollout record the parser turns into a message.
func codexAgentMessage(id, text string) obj {
	return obj{"timestamp": time.Now().Format(time.RFC3339Nano), "type": "event_msg",
		"payload": obj{"type": "item_completed", "item": obj{
			"type": "AgentMessage", "id": id,
			"content": []any{obj{"type": "Text", "text": text}}}}}
}

// appendCodexPadding grows a rollout past a size with records that carry no
// messages, standing in for the bulk of a long conversation.
func appendCodexPadding(t *testing.T, path string, want int64) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	line := func() []byte {
		record, err := json.Marshal(obj{
			"timestamp": time.Now().Format(time.RFC3339Nano),
			"type":      "turn_context",
			"payload":   obj{"pad": strings.Repeat("x", 4096)},
		})
		if err != nil {
			t.Fatal(err)
		}
		return append(record, '\n')
	}()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	for size := info.Size(); size < want; size += int64(len(line)) {
		if _, err := f.Write(line); err != nil {
			t.Fatal(err)
		}
	}
}

// Regression: stepping away for half an hour cost a session its history.
//
// The live window exists to stop a rollout nobody is running from lingering as
// a session, but a pane that is still open is proof its session is alive. Past
// the window the rollout was dropped anyway; the pane was then rediscovered as
// a session with no history at all, and activity re-attached it from scratch.
func TestCodexKeepsAPausedSessionBoundToItsOpenPane(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	rollout := writeCodexRollout(t, home, "thread-paused", "thread-paused", "/work/api",
		now.Add(-2*time.Hour), now.Add(-time.Minute), "task_complete")

	pane := tmux.Session{
		Name: "agentman-codex-1758900000000-ab12", Command: "codex",
		Cwd: "/work/api", PanePID: 4242, Created: now.Add(-3 * time.Hour),
	}
	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	src.processCheck = alwaysRunning
	src.listPanes = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{pane}, nil
	}

	first, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("got %d sessions on the first sweep, want one", len(first))
	}

	// Nothing typed for longer than the live window, pane still open.
	touch(t, rollout, now.Add(-45*time.Minute))
	second, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 {
		t.Fatalf("got %d sessions after the pause, want the one whose pane is open", len(second))
	}
	if second[0].ID != first[0].ID {
		t.Errorf("id changed across the pause: %s then %s", first[0].ID, second[0].ID)
	}
	src.mu.RLock()
	kept := src.sessions[second[0].ID]
	src.mu.RUnlock()
	if kept.transcript != rollout {
		t.Errorf("transcript = %q, want the rollout it was already reading", kept.transcript)
	}
}

// The other half of that guard: once the pane is gone, a rollout past the live
// window is an old conversation nobody is running, and must not linger.
func TestCodexDropsAPausedSessionOnceItsPaneIsGone(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	rollout := writeCodexRollout(t, home, "thread-paused", "thread-paused", "/work/api",
		now.Add(-2*time.Hour), now.Add(-time.Minute), "task_complete")

	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	src.processCheck = alwaysRunning
	src.listPanes = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{
			Name: "agentman-codex-1758900000000-ab12", Command: "codex",
			Cwd: "/work/api", PanePID: 4242, Created: now.Add(-3 * time.Hour),
		}}, nil
	}
	if _, err := src.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}

	touch(t, rollout, now.Add(-45*time.Minute))
	src.listPanes = noPanes
	found, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("got %d sessions, want none: the pane closed and the rollout is stale", len(found))
	}
}
