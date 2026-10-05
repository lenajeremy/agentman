package source

import (
	"bufio"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func acpUpdate(t *testing.T, s *CursorACPSource, st *cursorACPState, update string) {
	t.Helper()
	s.handle(st, cursorACPEnvelope{Method: "session/update", Params: json.RawMessage(`{"sessionId":"x","update":` + update + `}`)})
}

func acpRows(st *cursorACPState) []protocol.Message {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]protocol.Message(nil), st.record.Messages...)
}

func readACPReply(t *testing.T, replies *bufio.Reader) string {
	t.Helper()
	line, err := replies.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

// Cursor reports a command that exited non-zero as "completed", with the
// exit code in rawOutput. The row is one row from start to finish, named
// for the tool rather than for its title.
func TestCursorACPToolCallIsOneRowWithItsOutcome(t *testing.T) {
	s, st, _, _ := cursorACPFake(t)
	acpUpdate(t, s, st, `{"sessionUpdate":"tool_call","toolCallId":"t1","title":"`+"`ls missing`"+`","kind":"execute","status":"pending","rawInput":{"command":"ls missing"}}`)
	rows := acpRows(st)
	if len(rows) != 1 || rows[0].Tool.Name != "Shell" || rows[0].Tool.Summary != "ls missing" || rows[0].Tool.Status != protocol.ToolRunning {
		t.Fatalf("pending call = %+v", rows)
	}
	acpUpdate(t, s, st, `{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"in_progress"}`)
	acpUpdate(t, s, st, `{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed","rawOutput":{"exitCode":1,"stdout":"","stderr":"ls: missing: No such file or directory"}}`)
	rows = acpRows(st)
	if len(rows) != 1 {
		t.Fatalf("updates added rows: %+v", rows)
	}
	tool := rows[0].Tool
	if tool.Name != "Shell" || tool.Summary != "ls missing" || tool.Status != protocol.ToolError ||
		rows[0].Text != "Exit code 1\nls: missing: No such file or directory" {
		t.Fatalf("finished call = %+v %q", tool, rows[0].Text)
	}
}

func TestCursorACPEditShowsItsDiff(t *testing.T) {
	s, st, _, _ := cursorACPFake(t)
	acpUpdate(t, s, st, `{"sessionUpdate":"tool_call","toolCallId":"e1","title":"Edit `+"`notes.txt`"+`","kind":"edit","status":"pending","rawInput":{"path":"/w/notes.txt"},"locations":[{"path":"/w/notes.txt"}]}`)
	acpUpdate(t, s, st, `{"sessionUpdate":"tool_call_update","toolCallId":"e1","status":"completed","content":[{"type":"diff","path":"/w/notes.txt","oldText":"alpha\nbeta\ngamma\n","newText":"alpha\nBETA\ngamma\n"}]}`)
	rows := acpRows(st)
	if len(rows) != 1 || rows[0].Tool.Name != "Edit" || rows[0].Tool.Summary != "/w/notes.txt" || rows[0].Tool.Status != protocol.ToolOK {
		t.Fatalf("edit row = %+v", rows)
	}
	want := "--- a/w/notes.txt\n+++ b/w/notes.txt\n@@ -1,3 +1,3 @@\n alpha\n-beta\n+BETA\n gamma"
	if rows[0].Text != want {
		t.Fatalf("diff = %q, want %q", rows[0].Text, want)
	}
}

// Cursor's permission card has no rawInput: the command is the title and a
// write is a diff. The phone was shown an approval with an empty detail.
func TestCursorACPPermissionShowsWhatItApproves(t *testing.T) {
	s, st, _, _ := cursorACPFake(t)
	s.handle(st, cursorACPEnvelope{ID: json.RawMessage(`3`), Method: "session/request_permission", Params: json.RawMessage(`{
		"toolCall":{"toolCallId":"t2","title":"` + "`rm -rf build`" + `","kind":"execute","status":"pending",
			"content":[{"type":"content","content":{"type":"text","text":"Not in allowlist: rm"}}]},
		"options":[{"optionId":"allow-once","name":"Allow once"},{"optionId":"allow-always","name":"Allow always"},{"optionId":"reject-once","name":"Reject"}]}`)})
	q, _ := s.CurrentQuestion(context.Background(), cursorACPPrefix+st.record.NativeID)
	if q == nil || q.Title != "Shell command" || q.Prompt != "Run this command?" ||
		q.Detail != "rm -rf build\n\nNot in allowlist: rm" || len(q.Options) != 3 {
		t.Fatalf("shell permission = %+v", q)
	}

	s, st, _, _ = cursorACPFake(t)
	s.handle(st, cursorACPEnvelope{ID: json.RawMessage(`4`), Method: "session/request_permission", Params: json.RawMessage(`{
		"toolCall":{"toolCallId":"t3","title":"Write /w/new.txt","kind":"edit","status":"pending",
			"content":[{"type":"diff","path":"/w/new.txt","oldText":null,"newText":"hello\n"}]},
		"options":[{"optionId":"allow-once","name":"Allow once"},{"optionId":"reject-once","name":"Reject"}]}`)})
	q, _ = s.CurrentQuestion(context.Background(), cursorACPPrefix+st.record.NativeID)
	if q == nil || q.Title != "Edit file" || !strings.Contains(q.Detail, "--- /dev/null\n+++ b/w/new.txt\n@@ -0,0 +1,1 @@\n+hello") {
		t.Fatalf("write permission = %+v", q)
	}
}

// update_todos and generate_image arrive as requests that expect a reply;
// they used to be refused, and their content dropped.
func TestCursorACPTodosAndImagesReachTheirRows(t *testing.T) {
	s, st, _, replies := cursorACPFake(t)
	acpUpdate(t, s, st, `{"sessionUpdate":"tool_call","toolCallId":"todo1","title":"Update TODOs: a, b","kind":"other","status":"pending","rawInput":{"_toolName":"updateTodos","todos":[]}}`)
	s.handle(st, cursorACPEnvelope{ID: json.RawMessage(`9`), Method: "cursor/update_todos", Params: json.RawMessage(`{
		"toolCallId":"todo1","merge":false,"todos":[{"id":"1","content":"a","status":"completed"},{"id":"2","content":"b","status":"in_progress"}]}`)})
	if reply := readACPReply(t, replies); !strings.Contains(reply, `"id":9`) || !strings.Contains(reply, `"result":{}`) {
		t.Fatalf("update_todos reply = %s", reply)
	}
	acpUpdate(t, s, st, `{"sessionUpdate":"tool_call_update","toolCallId":"todo1","status":"completed"}`)
	rows := acpRows(st)
	if len(rows) != 1 || rows[0].Tool.Name != "TodoWrite" || rows[0].Text != "✔ a\n◐ b" {
		t.Fatalf("todo row = %+v", rows)
	}

	acpUpdate(t, s, st, `{"sessionUpdate":"tool_call","toolCallId":"img1","title":"Generate Image: a red square...","kind":"other","status":"pending","rawInput":{"_toolName":"generateImage","description":"a red square"}}`)
	s.handle(st, cursorACPEnvelope{ID: json.RawMessage(`10`), Method: "cursor/generate_image", Params: json.RawMessage(`{"toolCallId":"img1","description":"a red square","filePath":"/w/assets/red.png"}`)})
	if reply := readACPReply(t, replies); !strings.Contains(reply, `"id":10`) || strings.Contains(reply, "error") {
		t.Fatalf("generate_image reply = %s", reply)
	}
	acpUpdate(t, s, st, `{"sessionUpdate":"tool_call_update","toolCallId":"img1","status":"completed"}`)
	rows = acpRows(st)
	image := rows[len(rows)-1]
	if image.Tool.Name != "GenerateImage" || image.Tool.Summary != "/w/assets/red.png" || image.Text != "[image]" {
		t.Fatalf("image row = %+v %q", image.Tool, image.Text)
	}
}

func TestCursorACPPlanEntriesBecomeATodoRow(t *testing.T) {
	s, st, _, _ := cursorACPFake(t)
	acpUpdate(t, s, st, `{"sessionUpdate":"plan","entries":[{"content":"Write goodbye.txt","priority":"medium","status":"pending"}]}`)
	acpUpdate(t, s, st, `{"sessionUpdate":"plan","entries":[{"content":"Write goodbye.txt","priority":"medium","status":"completed"}]}`)
	rows := acpRows(st)
	if len(rows) != 1 || rows[0].Tool == nil || rows[0].Tool.Name != "TodoWrite" || rows[0].Text != "✔ Write goodbye.txt" {
		t.Fatalf("plan rows = %+v", rows)
	}
}

func TestCursorACPQuestionsCanBeSkipped(t *testing.T) {
	s, st, id, replies := cursorACPFake(t)
	s.handle(st, cursorACPEnvelope{ID: json.RawMessage(`11`), Method: "cursor/ask_question", Params: json.RawMessage(`{
		"toolCallId":"q1","title":"Before I start",
		"questions":[{"id":"word","prompt":"Which word?","allowMultiple":false,"options":[{"id":"bye","label":"bye"},{"id":"ciao","label":"ciao"}]}]}`)})
	q, _ := s.CurrentQuestion(context.Background(), id)
	if q == nil || len(q.Options) != 3 || q.Options[2].Key != cursorACPSkip {
		t.Fatalf("question = %+v", q)
	}
	if err := s.Answer(context.Background(), id, protocol.QuestionAnswer{QuestionID: q.ID, OptionKey: cursorACPSkip}); err != nil {
		t.Fatal(err)
	}
	if reply := readACPReply(t, replies); !strings.Contains(reply, `"outcome":"skipped"`) {
		t.Fatalf("skip reply = %s", reply)
	}
	if q, _ := s.CurrentQuestion(context.Background(), id); q != nil {
		t.Fatalf("question still pending after skip: %+v", q)
	}
}

func TestCursorACPRemembersTheMode(t *testing.T) {
	s, st, _, _ := cursorACPFake(t)
	acpUpdate(t, s, st, `{"sessionUpdate":"current_mode_update","currentModeId":"plan"}`)
	st.mu.Lock()
	mode := st.record.Mode
	st.mu.Unlock()
	if mode != "plan" {
		t.Fatalf("mode = %q", mode)
	}
}

// Discovery reports the ACP child's pid while a turn runs: its listening
// ports are this chat's dev servers.
func TestCursorACPReportsTheRunningProcess(t *testing.T) {
	s, st, _, _ := cursorACPFake(t)
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	st.mu.Lock()
	st.client.cmd = child
	st.mu.Unlock()
	sessions, err := s.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].AgentPID != child.Process.Pid {
		t.Fatalf("running turn reported %+v, want pid %d", sessions, child.Process.Pid)
	}
}
