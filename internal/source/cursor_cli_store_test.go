package source

import (
	"context"
	"os/exec"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// A page can end between a tool call and its result. The older page must
// still deliver the call settled: it is the only page that holds the call,
// and the app would otherwise show the command running forever.
func TestCursorCLIOlderPageSettlesACallWhoseResultIsNewer(t *testing.T) {
	_, source, store := cursorCLIFixture(t)
	sql := `INSERT INTO blobs(id,data) VALUES
('call', '{"role":"assistant","content":[{"type":"tool-call","toolCallId":"tc1\nfc_1","toolName":"Shell","args":{"command":"ls missing","description":"List"}}]}'),
('filler', '{"role":"assistant","content":[{"type":"text","text":"Still working."}]}'),
('result', '{"role":"tool","content":[{"type":"tool-result","toolCallId":"tc1\nfc_1","toolName":"Shell","result":"Exit code: 1"}],"providerOptions":{"cursor":{"highLevelToolCallResult":{"output":{"failure":{"command":"ls missing","exitCode":1,"interleavedOutput":"ls: missing: No such file"}},"isError":false}}}}'),
('after', '{"role":"assistant","content":[{"type":"text","text":"It is not there."}]}');`
	if output, err := exec.Command("sqlite3", store, sql).CombinedOutput(); err != nil {
		t.Fatalf("insert rows: %v: %s", err, output)
	}
	sessions, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id := sessions[0].ID
	latest, err := source.Page(context.Background(), id, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range latest.Messages {
		if m.Tool != nil {
			t.Fatalf("a result without its call made a row: %+v", m)
		}
	}
	older, err := source.Page(context.Background(), id, latest.NextCursor, 3)
	if err != nil {
		t.Fatal(err)
	}
	var call *protocol.Message
	for i := range older.Messages {
		if older.Messages[i].Tool != nil {
			call = &older.Messages[i]
		}
	}
	if call == nil || call.Tool.Summary != "ls missing" || call.Tool.Status != protocol.ToolError ||
		call.Text != "Exit code 1\nls: missing: No such file" {
		t.Fatalf("older page call = %+v", call)
	}
}

// An empty store prints nothing at all under sqlite3 -json; that is a chat
// with no messages yet, not a broken one.
func TestCursorCLIEmptyStoreIsAnEmptyPage(t *testing.T) {
	_, source, store := cursorCLIFixture(t)
	if output, err := exec.Command("sqlite3", store, "DELETE FROM blobs").CombinedOutput(); err != nil {
		t.Fatalf("empty store: %v: %s", err, output)
	}
	sessions, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	page, err := source.Page(context.Background(), sessions[0].ID, "", 10)
	if err != nil || len(page.Messages) != 0 {
		t.Fatalf("page = %+v, %v", page, err)
	}
}
