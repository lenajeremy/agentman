package question

import (
	"strings"
	"testing"
)

// Captured from Claude Code 2.1.289 asking to run `mkdir probe-dir`. The
// "always allow access to <folder>" choice wraps onto a second line, and
// Claude leaves a blank line after it. The scan for the menu stopped at that
// blank line, found options 3 and 4 without 1, and reported no question at
// all, so the phone could not see or answer the prompt.
func TestDetectsClaudesPermissionPromptWithAWrappedChoice(t *testing.T) {
	q := Detect(readFixture(t, "testdata/claude_permission_real_pane.txt"))
	if q == nil {
		t.Fatal("Claude's permission prompt was not detected")
	}
	if q.Prompt != "Do you want to proceed?" || len(q.Options) != 4 {
		t.Fatalf("detected %q with %d options: %+v", q.Prompt, len(q.Options), q.Options)
	}
	labels := []string{"Yes", "Yes, and always allow access to", "Yes, and switch to auto mode", "No"}
	for i, option := range q.Options {
		if option.Key != string(rune('1'+i)) || !strings.HasPrefix(option.Label, labels[i]) {
			t.Errorf("option %d = %+v, want key %d %q…", i, option, i+1, labels[i])
		}
	}
	if q.Title != "Bash command" || q.Detail != "Create probe-dir directory\n\nmkdir probe-dir" {
		t.Errorf("title %q, detail %q", q.Title, q.Detail)
	}
	if !q.Options[0].Selected || q.FocusIndex != 0 {
		t.Errorf("focus = %d; Yes is focused on screen", q.FocusIndex)
	}
	// "Tab to amend": the focused choice can take a note.
	if !q.AmendWithTab {
		t.Error("the prompt offers Tab to amend, and that was not seen")
	}
	if !strings.Contains(q.Options[1].Label+" "+q.Options[1].Description, "from this project") {
		t.Errorf("the wrapped choice lost its second line: %+v", q.Options[1])
	}
}

// One blank line inside a menu is layout. Crossing more would let the scan
// walk into an unrelated numbered list above the menu.
func TestABlankLineDoesNotJoinTwoLists(t *testing.T) {
	const pane = ` Here are the steps:

   1. Install

   2. Build


 Do you want to proceed?
 ❯ 3. Yes
   4. No

 Esc to cancel`
	if q := Detect(pane); q != nil {
		t.Errorf("joined a prose list and a menu across blank lines: %+v", q)
	}
}

// The same prompt with "No" focused, and then opened for a note with Tab, as
// Claude draws them.
func TestReadsTheFocusAndTheNoteRowOfClaudesPermissionPrompt(t *testing.T) {
	focused := Detect(readFixture(t, "testdata/claude_permission_no_focused_real_pane.txt"))
	if focused == nil || focused.FocusIndex != 3 || focused.Options[3].Label != "No" || !focused.AmendWithTab {
		t.Fatalf("with No focused: %+v", focused)
	}
	opened := Detect(readFixture(t, "testdata/claude_permission_amend_open_real_pane.txt"))
	if opened == nil || opened.FocusIndex != 3 ||
		opened.Options[3].Label != "No, and tell Claude what to do differently" || opened.AmendWithTab {
		t.Fatalf("with the note open: %+v", opened)
	}
	typed := Detect(readFixture(t, "testdata/claude_permission_amend_typed_real_pane.txt"))
	if typed == nil || typed.Options[3].Label != "No, name it probe-two instead" {
		t.Fatalf("with a note typed: %+v", typed)
	}
}
