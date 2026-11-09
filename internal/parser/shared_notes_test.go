package parser

import (
	"encoding/json"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func TestAgentmansNotesAreLeftOutOfTheChat(t *testing.T) {
	block := SharedNotesOpen + "\nFollow these.\n\n### Use pnpm\n\nAlways.\n" + SharedNotesClose
	for _, c := range []struct{ in, want string }{
		{"fix the login bug\n\n" + block, "fix the login bug"},
		{"fix the login bug", "fix the login bug"},
		{block, ""},
		{"cut short\n\n" + SharedNotesOpen + "\nFollow", "cut short"},
	} {
		if got := StripSharedNotes(c.in); got != c.want {
			t.Errorf("%q: got %q", c.in, got)
		}
	}
}

func TestEachAgentsRowLeavesTheNotesOut(t *testing.T) {
	block := "\n\n" + SharedNotesOpen + "\nnotes\n" + SharedNotesClose
	row, ok := MuseMessage("muse:a", MuseItem{ItemID: "1", Kind: "userMessage", Text: "hello" + block})
	if !ok || row.Text != "hello" {
		t.Errorf("muse: %q %v", row.Text, ok)
	}
	wrapped := "<timestamp>Monday</timestamp>\n<user_query>\nhello" + block + "\n</user_query>"
	if got := cursorCLIUserText(wrapped); got != "hello" {
		t.Errorf("cursor cli: %q", got)
	}
	if got := cleanCursorUserText(wrapped); got != "hello" {
		t.Errorf("cursor: %q", got)
	}
}

func TestAMuseCompactionShowsInTheChat(t *testing.T) {
	row, ok := MuseMessage("muse:a", MuseItem{ItemID: "1", Kind: "compaction", Status: "completed"})
	if !ok || row.Text != "Context compacted" {
		t.Fatalf("got %q, %v", row.Text, ok)
	}
	if _, ok := MuseMessage("muse:a", MuseItem{ItemID: "1", Kind: "compaction", Status: "running"}); ok {
		t.Fatal("shown before it finished")
	}
}

func TestAMuseChatKeepsWhatCameBeforeACompaction(t *testing.T) {
	var item MuseItem
	if err := json.Unmarshal([]byte(`{"itemId":"e","kind":"earlierHistory","earlierHistory":{"messages":[
		{"role":"user","text":"fix the bug"},{"role":"assistant","text":"Fixed."},{"role":"tool","text":"skip me"}]}}`), &item); err != nil {
		t.Fatal(err)
	}
	rows := MuseMessages("muse:a", []MuseItem{item})
	if len(rows) != 2 || rows[0].Text != "fix the bug" || rows[0].Role != protocol.RoleUser || rows[1].Text != "Fixed." || rows[0].ID == rows[1].ID {
		t.Fatalf("got %+v", rows)
	}
}
