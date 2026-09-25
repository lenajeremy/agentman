package source

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func stubHeaders(headers map[string]cursorHeader, err error) func(context.Context, string) (map[string]cursorHeader, error) {
	return func(context.Context, string) (map[string]cursorHeader, error) {
		return headers, err
	}
}

// touchStateDB plants a placeholder database so the stat gate lets the
// (stubbed) query through. The stub ignores file contents by design —
// production never queries a file this small without sqlite3 failing first.
func touchStateDB(t *testing.T, home string) {
	t.Helper()
	dbPath := cursorStateDBPath(home)
	if dbPath == "" {
		t.Skip("unknown platform layout")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCursorDiscoverMergesStateDB(t *testing.T) {
	home, _ := fakeCursorHome(t, "/decoded/proj", "sess-1")
	src, err := NewCursorSource(home)
	if err != nil {
		t.Fatal(err)
	}
	touchStateDB(t, home)
	var gotPath string
	src.queryHeaders = func(_ context.Context, dbPath string) (map[string]cursorHeader, error) {
		gotPath = dbPath
		return map[string]cursorHeader{
			"sess-1": {
				composerID: "sess-1",
				createdAt:  1788869805528,
				updatedAt:  1790253184344,
				subtitle:   "Check the docs for limits",
				blocking:   true,
				cwd:        "/exact/workspace/path",
			},
		}, nil
	}

	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if gotPath != cursorStateDBPath(home) {
		t.Errorf("queried unexpected db path: %q", gotPath)
	}
	// Exact workspace path wins over the lossy dash-decoding.
	if s.Cwd != "/exact/workspace/path" {
		t.Errorf("cwd should come from the index, got %q", s.Cwd)
	}
	// Cursor's own subtitle wins over the opening query.
	if s.Name != "Check the docs for limits" {
		t.Errorf("name should come from the index, got %q", s.Name)
	}
	if s.StartedAt != 1788869805528 || s.LastActivityAt != 1790253184344 {
		t.Errorf("timestamps should come from the index: %+v", s)
	}
	// The blocking flag surfaces even though transcripts never record it.
	// Question stays nil: visible and alerting, with nothing unanswerable
	// to tap.
	if s.State != protocol.StateWaitingInput {
		t.Errorf("blocking flag should read waiting_input, got %q", s.State)
	}
	if s.Question != nil {
		t.Errorf("no answer channel exists, question must stay nil: %+v", s.Question)
	}
}

func TestCursorDiscoverSkipsArchivedAndDegrades(t *testing.T) {
	home, _ := fakeCursorHome(t, "/live/proj", "live-1")
	archived := filepath.Join(home, ".cursor", "projects", "live-proj",
		"agent-transcripts", "gone-1", "gone-1.jsonl")
	writeJSONL(t, archived, []any{
		obj{"role": "user", "message": obj{"content": []any{
			obj{"type": "text", "text": "old question"}}}},
	})

	newSource := func() *CursorSource {
		src, err := NewCursorSource(home)
		if err != nil {
			t.Fatal(err)
		}
		return src
	}

	// Archived in the IDE stays hidden despite a fresh transcript.
	src := newSource()
	touchStateDB(t, home)
	src.queryHeaders = stubHeaders(map[string]cursorHeader{
		"gone-1": {composerID: "gone-1", archived: true},
	}, nil)
	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if s.NativeID == "gone-1" {
			t.Fatalf("archived session should stay hidden: %+v", s)
		}
	}

	// A failing index read degrades to transcript-only, never an error.
	src = newSource()
	touchStateDB(t, home)
	src.queryHeaders = stubHeaders(nil, context.DeadlineExceeded)
	sessions, err = src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected transcript-only fallback with 2 sessions, got %+v", sessions)
	}
}

// Cursor normally writes composer state into the WAL without touching the
// main database file. A cache keyed only by state.vscdb misses blocking and
// archival transitions until SQLite checkpoints the WAL.
func TestCursorHeadersRefreshWhenWALChanges(t *testing.T) {
	home := t.TempDir()
	touchStateDB(t, home)
	src, err := NewCursorSource(home)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	src.queryHeaders = func(context.Context, string) (map[string]cursorHeader, error) {
		calls++
		return map[string]cursorHeader{
			"sess-1": {subtitle: fmt.Sprintf("version %d", calls)},
		}, nil
	}
	read := func(wantCalls int) {
		t.Helper()
		headers := src.cursorHeaders(context.Background())
		if calls != wantCalls || headers["sess-1"].subtitle != fmt.Sprintf("version %d", wantCalls) {
			t.Fatalf("cache returned %+v after %d queries, want version %d", headers, calls, wantCalls)
		}
	}

	read(1)
	read(1) // no file changes: reuse the cached index
	walPath := cursorStateDBPath(home) + "-wal"
	if err := os.WriteFile(walPath, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	read(2) // WAL appeared; main DB is unchanged
	if err := os.WriteFile(walPath, []byte("second version"), 0o600); err != nil {
		t.Fatal(err)
	}
	read(3) // WAL grew
	if err := os.Remove(walPath); err != nil {
		t.Fatal(err)
	}
	read(4) // checkpoint removed the WAL
}

// TestCursorStateDBLive exercises the real sqlite3 subprocess against a
// scratch database. It runs only with CURSOR_LIVE_DBTEST=1 and a sqlite3
// binary present, following the repo's convention for environment-gated live
// tests.
func TestCursorStateDBLive(t *testing.T) {
	if os.Getenv("CURSOR_LIVE_DBTEST") == "" {
		t.Skip("set CURSOR_LIVE_DBTEST=1 to exercise the sqlite3 subprocess")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not present")
	}
	home, _ := fakeCursorHome(t, "/decoded/proj", "sess-1")
	dbPath := cursorStateDBPath(home)
	if dbPath == "" {
		t.Skip("unknown platform layout")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Note: inside the backtick Go string there is no escaping, so the SQL
	// string literal uses plain double quotes for its JSON.
	schema := `CREATE TABLE composerHeaders(composerId TEXT, workspaceId TEXT, createdAt INTEGER, lastUpdatedAt INTEGER, isArchived INTEGER, isSubagent INTEGER, recency INTEGER, checkpointAt INTEGER, value TEXT, subagentTypeName TEXT);
INSERT INTO composerHeaders VALUES('sess-1','ws1',1788869805528,1790253184344,0,0,1790253184344,NULL,'{"subtitle":"Live subtitle","unifiedMode":"agent","hasBlockingPendingActions":true,"workspaceIdentifier":{"uri":{"fsPath":"/live/ws"}}}', '');`
	cmd := exec.Command("sqlite3", dbPath, schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 setup: %v: %s", err, out)
	}

	src, err := NewCursorSource(home)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %+v", sessions)
	}
	s := sessions[0]
	if s.Name != "Live subtitle" || s.Cwd != "/live/ws" ||
		s.State != protocol.StateWaitingInput || s.LastActivityAt != 1790253184344 {
		t.Errorf("live index merge failed: %+v", s)
	}
}
