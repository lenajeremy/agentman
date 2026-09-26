package source

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
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
