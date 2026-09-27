package parser

import (
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// These lines are what Kiro CLI 2.24.1 wrote for a real turn: a prompt, a
// shell call it paused on for approval, the call's result, and the reply. The
// redacted thinking arrays are shortened; nothing else is.
const (
	fixtureKiroPrompt = `{"version":"v1","kind":"Prompt","data":{"message_id":"53874b54-cd1a-4be4-b91b-097aee95c608","content":[{"kind":"text","data":"Run the shell command: ls -la   then tell me how many files there are."}],"meta":{"timestamp":1790528359}}}`

	fixtureKiroToolCall = `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"65d2be17-211e-4c75-83ee-81c49dd27bd5","content":[{"kind":"thinking","data":{"text":"","signature":null,"redactedContent":[46,75,84,82],"modelId":"auto"}},{"kind":"text","data":""},{"kind":"toolUse","data":{"toolUseId":"tooluse_oH7ogPsxlQvCUqyUEeHdON","name":"shell","input":{"command":"ls -la","__tool_use_purpose":"List directory contents"}}}]}}`

	fixtureKiroToolResult = `{"version":"v1","kind":"ToolResults","data":{"message_id":"8a45bdd7-fc5f-44f3-8172-cfcc85af23b3","content":[{"kind":"toolResult","data":{"toolUseId":"tooluse_oH7ogPsxlQvCUqyUEeHdON","content":[{"kind":"json","data":{"exit_status":"exit status: 0","stdout":"total 8\n-rw-r--r--@ 1 mac  wheel   6 Sep 27 17:59 notes.txt\n","stderr":""}}],"status":"success"}}],"results":{}}}`

	fixtureKiroReply = `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"6780d9c3-1c85-422e-8d50-385ed87c53c0","content":[{"kind":"thinking","data":{"text":"","redactedContent":[46,75]}},{"kind":"text","data":"There is **1 file**: ` + "`notes.txt`" + `"}]}}`

	fixtureKiroInterrupted = `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"56a837fd-7eea-4485-b30e-350b3bda8c51","content":[{"kind":"text","data":"Response was interrupted by the user"}]}}`
)

func parseKiro(lines ...string) []protocol.Message {
	p := NewKiroParser("kiro:s")
	var out []protocol.Message
	for i, line := range lines {
		out = append(out, p.Parse(line, int64(i*1000))...)
	}
	return out
}

func TestKiroTurnBecomesPromptToolAndReply(t *testing.T) {
	got := parseKiro(fixtureKiroPrompt, fixtureKiroToolCall, fixtureKiroToolResult, fixtureKiroReply)

	// Running call, then the same call settled, then the reply. The empty
	// text block and the redacted thinking produce nothing.
	if len(got) != 4 {
		t.Fatalf("got %d messages, want 4: %+v", len(got), got)
	}
	if got[0].Role != protocol.RoleUser || !strings.HasPrefix(got[0].Text, "Run the shell command") {
		t.Errorf("prompt = %+v", got[0])
	}
	running, settled := got[1], got[2]
	if running.ID != "tooluse_oH7ogPsxlQvCUqyUEeHdON" || running.Tool == nil ||
		running.Tool.Status != protocol.ToolRunning {
		t.Errorf("call = %+v", running)
	}
	if running.Tool.Name != "Shell" || running.Tool.Summary != "ls -la" {
		t.Errorf("call rendered as %q %q, want Shell ls -la", running.Tool.Name, running.Tool.Summary)
	}
	if settled.ID != running.ID || settled.Tool.Status != protocol.ToolOK {
		t.Errorf("result did not settle the call: %+v", settled)
	}
	if settled.Tool.Summary != "ls -la" {
		t.Errorf("settled row lost its command: %+v", settled.Tool)
	}
	if !strings.Contains(settled.Text, "notes.txt") {
		t.Errorf("result preview = %q", settled.Text)
	}
	if got[3].Role != protocol.RoleAssistant || !strings.Contains(got[3].Text, "1 file") {
		t.Errorf("reply = %+v", got[3])
	}
}

// Regression guard for the reason the parser keeps a clock at all: only
// prompts are timestamped, and the app sorts by ts. A reply with no time of
// its own would render above the prompt that produced it.
func TestKiroRepliesSortAfterTheirPrompt(t *testing.T) {
	got := parseKiro(fixtureKiroPrompt, fixtureKiroToolCall, fixtureKiroToolResult, fixtureKiroReply, fixtureKiroPrompt, fixtureKiroInterrupted)

	promptTs := int64(1790528359) * 1000
	if got[0].Ts != promptTs {
		t.Errorf("prompt ts = %d, want %d", got[0].Ts, promptTs)
	}
	// The settled row replaces the running one by id, so it must keep the
	// call's position rather than moving to the time it finished.
	if got[1].Ts != got[2].Ts {
		t.Errorf("settled tool moved from %d to %d", got[1].Ts, got[2].Ts)
	}
	last := int64(0)
	for i, message := range []protocol.Message{got[0], got[1], got[3], got[4], got[5]} {
		if message.Ts <= last {
			t.Fatalf("message %d (%s) ts %d does not sort after %d", i, message.Role, message.Ts, last)
		}
		last = message.Ts
	}
}

func TestKiroInterruptedTurnIsShown(t *testing.T) {
	got := parseKiro(fixtureKiroPrompt, fixtureKiroInterrupted)
	if len(got) != 2 || got[1].Text != "Response was interrupted by the user" {
		t.Fatalf("got %+v", got)
	}
}

func TestKiroFailingCommandIsAnError(t *testing.T) {
	failed := strings.Replace(fixtureKiroToolResult, "exit status: 0", "exit status: 2", 1)
	got := parseKiro(fixtureKiroPrompt, fixtureKiroToolCall, failed)
	if got[2].Tool.Status != protocol.ToolError {
		t.Errorf("a non-zero exit was reported as %s", got[2].Tool.Status)
	}
}

func TestKiroFileWriteIsSummarizedByPathNotEditKind(t *testing.T) {
	write := `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m","content":[{"kind":"toolUse","data":{"toolUseId":"t1","name":"fs_write","input":{"command":"create","path":"/work/main.go","file_text":"package main"}}}]}}`
	got := parseKiro(fixtureKiroPrompt, write)
	if got[1].Tool.Name != "Write" || got[1].Tool.Summary != "/work/main.go" {
		t.Errorf("fs_write rendered as %q %q", got[1].Tool.Name, got[1].Tool.Summary)
	}
}
