package parser

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func TestAgentmansNotesAreLeftOutOfTheChat(t *testing.T) {
	head := SharedNotesOpen + "\n" + SharedNotesHeader + " follow these.\n"
	block := head + "\n### Use pnpm\n\nAlways.\n" + SharedNotesClose
	for _, c := range []struct{ in, want string }{
		{"fix the login bug\n\n" + block, "fix the login bug"},
		{"fix the login bug", "fix the login bug"},
		{block, ""},
		{"cut short\n\n" + head + "\n### Use", "cut short"},
		// A note that quotes the closing tag.
		{"hello\n\n" + head + "\nWrite " + SharedNotesClose + " like so.\n" + SharedNotesClose, "hello"},
		// Someone talking about the tag.
		{"what does " + SharedNotesOpen + " do?", "what does " + SharedNotesOpen + " do?"},
	} {
		if got := StripSharedNotes(c.in); got != c.want {
			t.Errorf("%q: got %q", c.in, got)
		}
	}
}

func TestEachAgentsRowLeavesTheNotesOut(t *testing.T) {
	block := "\n\n" + SharedNotesOpen + "\n" + SharedNotesHeader + " notes\n" + SharedNotesClose
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
	if got := cursorCLIUserText("hello" + block); got != "hello" {
		t.Errorf("cursor cli without a wrapper: %q", got)
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
	if !strings.HasPrefix(rows[0].ID, MuseEarlierPrefix) {
		t.Fatalf("id %q doesn't say it is a copy", rows[0].ID)
	}
	// Before everything else, wherever Muse dates the item.
	item.RecordedAt = "2026-11-09T15:00:00Z"
	after := MuseItem{ItemID: "n", Kind: "agentMessage", Text: "Next.", RecordedAt: "2026-11-09T14:00:00Z"}
	placed := MuseMessages("muse:a", []MuseItem{item, after})
	if len(placed) != 3 || !(placed[0].Ts < placed[1].Ts && placed[1].Ts < placed[2].Ts) {
		t.Fatalf("got %+v", placed)
	}
}
