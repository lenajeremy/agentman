package parser

import "testing"

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
