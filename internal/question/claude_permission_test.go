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

// Claude Code counts down on a prompt it will block by itself if nobody
// answers. The clock was read as part of the question, so every second was
// a new one: a card and a notification every second or two, and the user's
// answer refused as "no longer on screen" because the clock had moved on.
func TestAPromptsCountdownIsNotPartOfTheQuestion(t *testing.T) {
	pane := func(clock string) string {
		return `────────────────────────────────────────────────────────────────────────────────
 Bash command

   rm -f "$T/$f"
   Remove the test's socket files

 Dangerous rm operation on possibly-empty variable path: "$T/$f"
 ⚠ Claude Code will automatically block this request in ` + clock + `, to avoid holding up an unattended session

 Do you want to proceed?
   1. Yes
 ❯ 2. No

 Esc to cancel · Tab to amend`
	}
	first, later := Detect(pane("1:35")), Detect(pane("0:59"))
	if first == nil || later == nil {
		t.Fatal("the prompt was not detected")
	}
	if first.Detail != later.Detail || first.Prompt != later.Prompt || first.Title != later.Title {
		t.Errorf("the question changed with the clock:\n%q\n%q", first.Detail, later.Detail)
	}
	if !strings.Contains(first.Detail, "will automatically block this request soon, to avoid") {
		t.Errorf("detail = %q", first.Detail)
	}
	if !strings.Contains(first.Detail, `rm -f "$T/$f"`) || first.Prompt != "Do you want to proceed?" || len(first.Options) != 2 {
		t.Errorf("question = %+v", first)
	}
}
