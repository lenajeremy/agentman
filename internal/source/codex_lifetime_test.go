package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
