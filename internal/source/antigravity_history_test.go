package source

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeAntigravityIndex builds conversation_summaries.db the way agy 1.2.17
// lays it out, with the sqlite3 CLI the adapter itself reads it with.
func writeAntigravityIndex(t *testing.T, home string, rows ...string) {
	t.Helper()
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 is not installed")
	}
	root := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// agy keeps the database in WAL mode. With agy not running there is no
	// -wal or -shm beside it, which is the case a read-only open cannot
	// manage without being told the file will not change.
	script := "PRAGMA journal_mode=WAL;\nCREATE TABLE `conversation_summaries` (`conversation_id` text,`title` text NOT NULL DEFAULT \"\"," +
		"`preview` text NOT NULL DEFAULT \"\",`step_count` integer NOT NULL DEFAULT 0," +
		"`last_modified_time` datetime NOT NULL,`workspace_uris` text NOT NULL,`status` text NOT NULL DEFAULT \"\"," +
		"`source` text NOT NULL DEFAULT \"\",`project_id` text NOT NULL DEFAULT \"\",`agent_name` text NOT NULL DEFAULT \"\"," +
		"`parent_conversation_id` text NOT NULL DEFAULT \"\",`nesting_depth` integer NOT NULL DEFAULT 0," +
		"`last_user_input_time` datetime NOT NULL,PRIMARY KEY (`conversation_id`));\n"
	for _, row := range rows {
		script += row + ";\n"
	}
	db := filepath.Join(root, "conversation_summaries.db")
	cmd := exec.Command(bin, db)
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}
	// What agy leaves once it exits: everything checkpointed into the
	// database, and no -wal or -shm beside it.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(db + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func agyIndexRow(id, title, modified, workspace, agent, parent string) string {
	uris := "[]"
	if workspace != "" {
		uris = fmt.Sprintf(`["file://%s"]`, workspace)
	}
	return fmt.Sprintf("INSERT INTO conversation_summaries (conversation_id, title, last_modified_time, workspace_uris, "+
		"status, agent_name, parent_conversation_id, last_user_input_time) VALUES ('%s', '%s', '%s', '%s', "+
		"'CASCADE_RUN_STATUS_IDLE', '%s', '%s', '%s')", id, title, modified, uris, agent, parent, modified)
}

// agy's own index has every conversation with its workspace and the title its
// /resume picker shows, including ones no prompt was typed into — and marks
// subagents, which are their parent's work rather than sessions of their own.
func TestAntigravityPastReadsAgysConversationIndex(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "code", "api")
	const (
		titled   = "ef116096-84c3-4723-bbc3-aed0f3cca68b"
		subagent = "659d2256-2d1f-4ecf-9f18-e0452e64434e"
		logged   = "16d0a7e5-165d-4de6-a2d4-bd791f805bdf"
		older    = "0cf8ac1d-48f6-4aee-b760-80b7dd1eb885"
	)
	writeAntigravityIndex(t, home,
		agyIndexRow(titled, "Create And Edit Notes File", "2026-10-05 09:37:52.321166+00:00", work, "", ""),
		agyIndexRow(subagent, "", "2026-10-05 09:40:16.1+00:00", work, "research", titled),
		// Started in a folder agy had not been told to trust: no workspace in
		// the index, but the prompt log has it.
		agyIndexRow(logged, "Locate Single File Name", "2026-10-05 09:50:00+00:00", "", "", ""),
	)
	root := filepath.Join(home, ".gemini", "antigravity-cli")
	history := fmt.Sprintf(`{"display":"Tell me the name of the only file","timestamp":1791193800000,"workspace":%q,"conversationId":%q}`+"\n"+
		`{"display":"an older one","timestamp":1790000000000,"workspace":%q,"conversationId":%q}`+"\n",
		work, logged, work, older)
	if err := os.WriteFile(filepath.Join(root, "history.jsonl"), []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := NewAntigravitySource(home)
	if err != nil {
		t.Fatal(err)
	}
	past, err := s.Past(context.Background(), work, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, session := range past {
		got[session.NativeID] = session.Name
		if session.Cwd != work {
			t.Errorf("%s: cwd %q", session.NativeID, session.Cwd)
		}
	}
	want := map[string]string{
		titled: "Create And Edit Notes File",
		logged: "Locate Single File Name",
		older:  "an older one",
	}
	if len(got) != len(want) {
		t.Fatalf("past = %v, want %v", got, want)
	}
	for id, name := range want {
		if got[id] != name {
			t.Errorf("%s named %q, want %q", id, got[id], name)
		}
	}
	// Newest first, by the index's own clock.
	if past[0].NativeID != logged || past[1].NativeID != titled || past[1].LastActivityAt != 1791193072321 {
		t.Errorf("order %s %s, last activity %d", past[0].NativeID, past[1].NativeID, past[1].LastActivityAt)
	}

	folders, err := s.Directories(context.Background())
	if err != nil || len(folders) != 1 || folders[0].Path != work || folders[0].Agents != 3 {
		t.Errorf("folders = %+v, %v", folders, err)
	}
}

// A database that cannot be read this moment is not an empty one.
func TestAntigravityIndexKeepsItsLastGoodRead(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "code", "api")
	writeAntigravityIndex(t, home,
		agyIndexRow("ef116096-84c3-4723-bbc3-aed0f3cca68b", "Notes", "2026-10-05 09:37:52+00:00", work, "", ""))
	s, err := NewAntigravitySource(home)
	if err != nil {
		t.Fatal(err)
	}
	if past, _ := s.Past(context.Background(), work, 0); len(past) != 1 {
		t.Fatalf("past = %+v", past)
	}
	db := filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")
	if err := os.WriteFile(db, []byte("not a database, mid-rewrite"), 0o644); err != nil {
		t.Fatal(err)
	}
	if past, _ := s.Past(context.Background(), work, 0); len(past) != 1 {
		t.Errorf("an unreadable index emptied history: %+v", past)
	}
}

func TestAntigravityIndexFields(t *testing.T) {
	if got := antigravityWorkspace(`["file:///Users/me/My%20Work/api"]`); got != "/Users/me/My Work/api" {
		t.Errorf("workspace = %q", got)
	}
	if got := antigravityWorkspace(`[]`); got != "" {
		t.Errorf("no workspace = %q", got)
	}
	if got := antigravityIndexTime("2026-10-05 09:37:52.321166+00:00"); got != 1791193072321 {
		t.Errorf("time = %d", got)
	}
}
