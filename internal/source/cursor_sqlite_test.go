package source

import (
	"context"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// walDatabase builds a WAL-mode database the way Cursor's are, then removes
// its -wal and -shm files, as happens when it is closed by a SQLite build that
// cleans them up, or copied without them. A read-only open then fails with
// "unable to open database file" unless it is opened as immutable.
func walDatabase(t *testing.T, path, sql string) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not installed")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("sqlite3", path, "PRAGMA journal_mode=WAL;"+sql).CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v: %s", path, err, output)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if output, err := exec.Command("sqlite3", "-readonly", "file:"+path+"?mode=ro", "SELECT 1").CombinedOutput(); err == nil {
		t.Skipf("this sqlite3 reads a WAL database without its -wal file (%s); nothing to guard", output)
	}
}

func TestCursorStoresReadWithoutTheirWAL(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "project")
	writeCursorCLIChat(t, home, "hash", "chat-1", cwd, "Closed chat", 1)
	store := filepath.Join(home, ".cursor", "chats", "hash", "chat-1", "store.db")
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	meta := hex.EncodeToString([]byte(`{"mode":"plan"}`))
	walDatabase(t, store, `CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB);
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT);
INSERT INTO meta VALUES ('0','`+meta+`');
INSERT INTO blobs VALUES ('u','{"role":"user","content":[{"type":"text","text":"<user_query>hello</user_query>"}]}');
INSERT INTO blobs VALUES ('a','{"role":"assistant","content":[{"type":"reasoning","text":"","providerOptions":{"cursor":{"modelName":"example-model"}}},{"type":"text","text":"hi"}]}');`)

	source, err := NewCursorCLISource(home)
	if err != nil {
		t.Fatal(err)
	}
	past, err := source.Past(context.Background(), cwd, 0)
	if err != nil || len(past) != 1 {
		t.Fatalf("past = %+v, %v", past, err)
	}
	page, err := source.Page(context.Background(), past[0].ID, "", 10)
	if err != nil || len(page.Messages) != 2 || page.Messages[1].Text != "hi" {
		t.Fatalf("page of a closed WAL store = %+v, %v", page, err)
	}
	if model := queryCursorCLIModel(context.Background(), store); model != "example-model" {
		t.Errorf("model = %q", model)
	}
	if mode := queryCursorCLIStoreMode(context.Background(), store); mode != "plan" {
		t.Errorf("mode = %q", mode)
	}
	if _, err := os.Stat(store + "-wal"); !os.IsNotExist(err) {
		t.Error("reading the store created a -wal file in Cursor's directory")
	}
}

func TestCursorIDEIndexReadsWithoutItsWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.vscdb")
	walDatabase(t, path, `CREATE TABLE composerHeaders (composerId TEXT, createdAt INTEGER, lastUpdatedAt INTEGER, isArchived INTEGER, value TEXT);
INSERT INTO composerHeaders VALUES ('c1', 1, 2, 0, '{"subtitle":"Fix the build"}');`)
	headers, err := queryCursorHeaders(context.Background(), path)
	if err != nil || headers["c1"].subtitle != "Fix the build" {
		t.Fatalf("headers = %+v, %v", headers, err)
	}
}
