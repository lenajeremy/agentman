package parser

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// testdata/cursor-cli-store.jsonl holds one JSON blob per line in the shapes
// Cursor CLI 2026.09.26 writes to a chat store, synthesised from a real
// chat: every tool family, a rejected call, a failed and an interrupted
// command, a result Cursor rewrote, an image attached to a prompt and a mode
// change.
func cursorCLIStoreRows(t *testing.T) []CursorCLIRow {
	t.Helper()
	file, err := os.Open("testdata/cursor-cli-store.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var rows []CursorCLIRow
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		rows = append(rows, CursorCLIRow{RowID: int64(len(rows) + 1), ID: fmt.Sprintf("blob-%d", len(rows)+1), Data: scanner.Text()})
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

type cursorCLIWant struct {
	role    protocol.Role
	name    string
	summary string
	status  protocol.ToolStatus
	text    string // prefix of Text
}

func TestCursorCLIStoreBecomesTheFeed(t *testing.T) {
	got := CursorCLIMessages("cursor-cli:chat:c", 1000, cursorCLIStoreRows(t))
	want := []cursorCLIWant{
		{role: protocol.RoleUser, text: "Create hello.txt, then run echo hi."},
		{role: protocol.RoleAssistant, text: "Creating `hello.txt`"},
		{role: protocol.RoleTool, name: "Write", summary: "/w/hello.txt", status: protocol.ToolOK, text: "--- /dev/null\n+++ b/w/hello.txt\n@@ -1,0 +1 @@\n+hi"},
		{role: protocol.RoleTool, name: "Shell", summary: "echo hi", status: protocol.ToolError, text: "Rejected"},
		{role: protocol.RoleAssistant, text: "Retrying."},
		{role: protocol.RoleTool, name: "Shell", summary: "echo hi", status: protocol.ToolOK, text: "hi"},
		{role: protocol.RoleTool, name: "Shell", summary: "ls missing", status: protocol.ToolError, text: "Exit code 1\nls: missing"},
		{role: protocol.RoleTool, name: "Shell", summary: "sleep 30", status: protocol.ToolError, text: "Error: Shell command was interrupted"},
		{role: protocol.RoleTool, name: "Edit", summary: "/w/notes.txt", status: protocol.ToolOK, text: "--- a/w/notes.txt\n+++ b/w/notes.txt\n@@ -1,3 +1,3 @@\n alpha\n-beta\n+BETA"},
		{role: protocol.RoleTool, name: "GetDynamicTools", summary: "cursor · TodoWrite", status: protocol.ToolOK},
		{role: protocol.RoleTool, name: "TodoWrite", status: protocol.ToolOK, text: "✔ Replace beta\n◐ Delete hello.txt"},
		{role: protocol.RoleTool, name: "Delete", summary: "/w/hello.txt", status: protocol.ToolOK, text: "--- a/w/hello.txt\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-hi"},
		{role: protocol.RoleUser, text: "What colour is red.png?"},
		{role: protocol.RoleTool, name: "Read", summary: "/w/red.png", status: protocol.ToolOK, text: "[image]"},
		{role: protocol.RoleTool, name: "Read", summary: "/w/missing.txt", status: protocol.ToolError, text: "File not found"},
		{role: protocol.RoleAssistant, text: "Red"},
		{role: protocol.RoleSystem, text: "Switched to Plan mode"},
		{role: protocol.RoleUser, text: "Plan goodbye.txt"},
		{role: protocol.RoleTool, name: "CreatePlan", summary: "Create goodbye.txt", status: protocol.ToolOK, text: "# Create goodbye.txt"},
		{role: protocol.RoleTool, name: "search_issues", summary: `github: {"query":"is:open label:bug"}`, status: protocol.ToolRunning},
		{role: protocol.RoleTool, name: "Grep", summary: "goodbye", status: protocol.ToolRunning},
	}
	if len(got) != len(want) {
		for _, m := range got {
			t.Logf("%s %+v %q", m.Role, m.Tool, m.Text)
		}
		t.Fatalf("got %d messages, want %d", len(got), len(want))
	}
	ids := map[string]bool{}
	for i, w := range want {
		m := got[i]
		if ids[m.ID] {
			t.Errorf("%d: duplicate id %q", i, m.ID)
		}
		ids[m.ID] = true
		if m.Role != w.role || !strings.HasPrefix(m.Text, w.text) {
			t.Errorf("%d: %s %q, want %s starting %q", i, m.Role, m.Text, w.role, w.text)
		}
		if w.role != protocol.RoleTool {
			continue
		}
		if m.Tool == nil || m.Tool.Name != w.name || m.Tool.Summary != w.summary || m.Tool.Status != w.status {
			t.Errorf("%d: tool %+v, want %s %q %s", i, m.Tool, w.name, w.summary, w.status)
		}
		if strings.Contains(m.ID, "\n") {
			t.Errorf("%d: id %q carries Cursor's newline", i, m.ID)
		}
	}
	for _, m := range got {
		if strings.Contains(m.Text, "<user_info>") || strings.Contains(m.Text, "image_files") ||
			strings.Contains(m.Text, "Command output:") || strings.Contains(m.Text, "system prompt") {
			t.Errorf("scaffolding leaked into the feed: %q", m.Text)
		}
	}
}

// A call and its result are one row: the running row and the settled one
// share an id, so the app replaces one with the other.
func TestCursorCLIToolRowSettlesInPlace(t *testing.T) {
	rows := cursorCLIStoreRows(t)
	running := CursorCLIMessages("s", 0, rows[:4])
	settled := CursorCLIMessages("s", 0, rows[:6])
	find := func(messages []protocol.Message, name string) protocol.Message {
		for _, m := range messages {
			if m.Tool != nil && m.Tool.Name == name {
				return m
			}
		}
		t.Fatalf("no %s row", name)
		return protocol.Message{}
	}
	before, after := find(running, "Write"), find(settled, "Write")
	if before.ID != after.ID || before.Tool.Status != protocol.ToolRunning || after.Tool.Status != protocol.ToolOK {
		t.Fatalf("running %+v / settled %+v", before.Tool, after.Tool)
	}
	if after.Tool.Summary != before.Tool.Summary {
		t.Fatal("settling the row lost its summary")
	}
}

// An older page can end between a call and its result; the source fetches
// those results, and only calls whose results are missing need it.
func TestCursorCLIPendingCallsAreTheOnesWithoutResults(t *testing.T) {
	rows := cursorCLIStoreRows(t)
	if got := CursorCLIPendingCalls(rows); strings.Join(got, ",") != "call-u,call-v" {
		t.Fatalf("pending = %q", got)
	}
	if got := CursorCLIPendingCalls(rows[:4]); len(got) != 2 {
		t.Fatalf("pending on a page that stops before the results = %q", got)
	}
}

func TestCursorCLIModelComesFromTheReply(t *testing.T) {
	rows := cursorCLIStoreRows(t)
	if got := CursorCLIModel(rows[3].Data); got != "cursor-grok-4.5-high-fast" {
		t.Fatalf("model = %q", got)
	}
	if got := CursorCLIModel(rows[2].Data); got != "" {
		t.Fatalf("a prompt reported model %q", got)
	}
}
