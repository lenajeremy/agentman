package parser

import (
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func TestMuseMessagesKeepWhatAPersonReads(t *testing.T) {
	one := 1
	items := []MuseItem{
		{ItemID: "u1", Kind: "userMessage", Text: "Fix the login page", RecordedAt: "2026-10-31T23:00:00Z"},
		{ItemID: "r1", Kind: "reminderChild", Text: "goal reminder"},
		{ItemID: "t1", Kind: "toolCall", Tool: "read_file", Args: `{"path":"src/login.tsx"}`, Status: "completed",
			DisplayLabel: &MuseDisplayLabel{Active: "Reading login.tsx", Done: "Read login.tsx"}},
		{ItemID: "t2", Kind: "toolCall", Tool: "bash", CommandText: "npm test", Status: "completed", ExitCode: &one},
		{ItemID: "t3", Kind: "toolCall", Tool: "edit", Status: "inProgress",
			DisplayLabel: &MuseDisplayLabel{Active: "Editing login.tsx", Done: "Edited login.tsx"}},
		{ItemID: "a1", Kind: "agentMessage", Text: "Fixed it.\n"},
		{ItemID: "u2", Kind: "userMessage", Text: "never mind", Retracted: true},
		{ItemID: "h1", Kind: "hookRun"},
	}
	got := MuseMessages("muse:s", items)
	if len(got) != 5 {
		t.Fatalf("got %d rows, want 5: %+v", len(got), got)
	}
	if got[0].Role != protocol.RoleUser || got[0].Text != "Fix the login page" || got[0].Ts == 0 {
		t.Errorf("user row: %+v", got[0])
	}
	if tool := got[1].Tool; tool == nil || tool.Title != "Read login.tsx" || tool.Summary != "src/login.tsx" || tool.Status != protocol.ToolOK {
		t.Errorf("read row: %+v", got[1].Tool)
	}
	if tool := got[2].Tool; tool.Summary != "npm test" || tool.Status != protocol.ToolError {
		t.Errorf("a failed command should read as an error: %+v", tool)
	}
	if tool := got[3].Tool; tool.Title != "Editing login.tsx" || tool.Status != protocol.ToolRunning {
		t.Errorf("a running tool keeps its present tense: %+v", tool)
	}
	if got[4].Role != protocol.RoleAssistant || got[4].Text != "Fixed it." {
		t.Errorf("agent row: %+v", got[4])
	}
}
