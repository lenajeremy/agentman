package source

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// openCodeFixtureDB builds a store shaped like OpenCode's own (only the
// columns read here), with rows shaped like the real ones: message and part
// data carry the API's JSON without their ids.
func openCodeFixtureDB(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not installed")
	}
	path := filepath.Join(t.TempDir(), "opencode.db")
	statements := []string{
		`CREATE TABLE session (id text PRIMARY KEY, parent_id text, directory text NOT NULL, title text NOT NULL,
			model text, time_created integer NOT NULL, time_updated integer NOT NULL, time_archived integer);`,
		`CREATE TABLE message (id text PRIMARY KEY, session_id text NOT NULL, time_created integer NOT NULL, data text NOT NULL);`,
		`CREATE TABLE part (id text PRIMARY KEY, message_id text NOT NULL, session_id text NOT NULL, data text NOT NULL);`,
		`INSERT INTO session VALUES ('ses_trip', NULL, '/work/app', 'Plan the release',
			'{"id":"big-pickle","providerID":"opencode"}', 1000, 5000, NULL);`,
		`INSERT INTO session VALUES ('ses_child', 'ses_trip', '/work/app', 'subagent', NULL, 1100, 1200, NULL);`,
		`INSERT INTO session VALUES ('ses_gone', NULL, '/work/app', 'archived', NULL, 900, 950, 960);`,
		`INSERT INTO session VALUES ('ses_web', NULL, '/work/web', 'Fix the header', NULL, 2000, 3000, NULL);`,
		`INSERT INTO message VALUES ('msg_1', 'ses_trip', 1000, '{"role":"user","time":{"created":1000}}');`,
		`INSERT INTO message VALUES ('msg_2', 'ses_trip', 2000, '{"role":"assistant","modelID":"big-pickle","time":{"created":2000}}');`,
		`INSERT INTO message VALUES ('msg_3', 'ses_trip', 3000, '{"role":"user","time":{"created":3000}}');`,
		`INSERT INTO part VALUES ('prt_1', 'msg_1', 'ses_trip', '{"type":"text","text":"ship it"}');`,
		`INSERT INTO part VALUES ('prt_2', 'msg_2', 'ses_trip', '{"type":"text","text":"Shipped."}');`,
		`INSERT INTO part VALUES ('prt_3', 'msg_3', 'ses_trip', '{"type":"text","text":"thanks"}');`,
	}
	command := exec.Command("sqlite3", path, strings.Join(statements, "\n"))
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("building the fixture: %v: %s", err, out)
	}
	return path
}

func storeOnlyOpenCode(t *testing.T) *OpenCodeSource {
	t.Helper()
	db := openCodeFixtureDB(t)
	s := NewOpenCodeSource("")
	s.findServers = func(context.Context) []string { return nil }
	s.store = &openCodeStore{path: func() string { return db }}
	return s
}

// With no `opencode serve` running, every OpenCode session vanished from the
// phone — folders, history and transcripts — although all of them were still
// in OpenCode's own database.
func TestOpenCodeHistoryWithNoServerComesFromItsStore(t *testing.T) {
	s := storeOnlyOpenCode(t)
	ctx := context.Background()

	past, err := s.Past(ctx, "/work/app", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 || past[0].NativeID != "ses_trip" {
		t.Fatalf("Past = %+v, want only the conversation (no subagent, nothing archived)", past)
	}
	if past[0].Name != "Plan the release" || past[0].State != protocol.StateEnded ||
		past[0].Model == "" || past[0].LastActivityAt != 5000 {
		t.Errorf("history row = %+v", past[0])
	}

	folders, err := s.Directories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, folder := range folders {
		counts[folder.Path] = folder.Agents
	}
	if counts["/work/app"] != 1 || counts["/work/web"] != 1 {
		t.Errorf("folders = %v", counts)
	}
}

func TestOpenCodeTranscriptWithNoServerPagesFromItsStore(t *testing.T) {
	s := storeOnlyOpenCode(t)
	ctx := context.Background()
	if _, err := s.Past(ctx, "/work/app", 0); err != nil {
		t.Fatal(err)
	}
	const id = "opencode:ses_trip"

	newest, err := s.Page(ctx, id, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(newest.Messages) != 2 || newest.Messages[0].Text != "Shipped." ||
		newest.Messages[1].Text != "thanks" || !newest.HasMore {
		t.Fatalf("newest page = %+v", newest)
	}
	older, err := s.Page(ctx, id, newest.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Messages) != 1 || older.Messages[0].Text != "ship it" || older.HasMore {
		t.Fatalf("older page = %+v", older)
	}
}

// Ids and cursors are spliced into the query, so anything that is not one is
// refused rather than run.
func TestOpenCodeStoreRefusesWhatIsNotAnID(t *testing.T) {
	s := storeOnlyOpenCode(t)
	for _, tc := range []struct{ native, before string }{
		{"ses_trip' OR '1'='1", ""},
		{"ses_trip", "1:msg_1' OR '1'='1"},
		{"ses_trip", "soon"},
	} {
		if _, _, err := s.store.messages(context.Background(), tc.native, tc.before, 10); err == nil {
			t.Errorf("accepted %q / %q", tc.native, tc.before)
		}
	}
}
