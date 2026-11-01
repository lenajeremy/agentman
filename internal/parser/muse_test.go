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

func TestMuseToolTitlesSayWhatHappened(t *testing.T) {
	items := []MuseItem{
		// Muse's model describes each command; history keeps no label.
		{ItemID: "b1", Kind: "toolCall", Tool: "bash", Status: "rejected",
			Args: `{"command": "echo hello > note.txt", "description": "Write a note", "sandbox_permissions": "require_escalated"}`},
		// A refused edit is not "Edited".
		{ItemID: "e1", Kind: "toolCall", Tool: "edit_file", Status: "failed", Args: `{"path": "a.go"}`,
			DisplayLabel: &MuseDisplayLabel{Active: "Editing a.go", Done: "Edited a.go", Neutral: "Edit a.go"}},
		{ItemID: "q1", Kind: "toolCall", Tool: "request_user_input", Status: "completed",
			Args: `{"questions": [{"id": "color", "header": "Color", "question": "Which colour should the button be?", "options": [{"label": "Blue"}, {"label": "Green"}]}, {"id": "extras", "header": "Extras", "question": "Which extras?", "options": [{"label": "Shadow"}, {"label": "Icon"}]}]}`},
	}
	got := MuseMessages("muse:s", items)
	if len(got) != 3 {
		t.Fatalf("rows: %+v", got)
	}
	if tool := got[0].Tool; tool.Title != "Wrote a note" || tool.Summary != "echo hello > note.txt" || tool.Status != protocol.ToolError {
		t.Errorf("described command: %+v", tool)
	}
	if tool := got[1].Tool; tool.Title != "Edit a.go" {
		t.Errorf("failed edit title: %+v", tool)
	}
	if tool := got[2].Tool; tool.Title != "Asked 2 questions" || tool.Summary != "Which colour should the button be? (+1 more)" {
		t.Errorf("question row: %+v", tool)
	}
}

func TestMuseTurnEndedNamesStopsAndFailures(t *testing.T) {
	if _, ok := MuseTurnEnded("muse:s", MuseTurnEnd{TurnID: "t", Terminal: "completed"}, 1); ok {
		t.Error("a finished turn needs no row")
	}
	if row, ok := MuseTurnEnded("muse:s", MuseTurnEnd{TurnID: "t", Terminal: "cancelled", Reason: "cancelled during model step"}, 1); !ok ||
		row.Text != "Interrupted" || row.Role != protocol.RoleSystem || row.ID != "turn:t:cancelled" {
		t.Errorf("stopped turn: %+v", row)
	}
	if row, ok := MuseTurnEnded("muse:s", MuseTurnEnd{TurnID: "t", Terminal: "failed", Reason: "model unavailable"}, 1); !ok ||
		row.Text != "Muse turn failed: model unavailable" {
		t.Errorf("failed turn: %+v", row)
	}
}
