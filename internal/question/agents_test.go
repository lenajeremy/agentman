package question

import "testing"

// Screens captured from Kiro CLI 2.24.1 and Antigravity CLI 1.2.12 in a
// 160-column tmux pane. The long rules are shortened; nothing else is.
const (
	kiroApprovalPane = `────────────────────────────────────────
  Run the shell command: ls -la   then tell me how many files there are.
↓ Shell ls -la
────────────────────────────────────────
 shell requires approval
 ❯ Yes, single permission
   Trust, always allow in this session
   No (Tab to edit)
────────────────────────────────────────
 esc to close · ↑↓ to navigate · ↵ to select · Tab to edit
`

	kiroIdlePane = `• There is 1 file: notes.txt
  1. notes.txt
  2. a second line that only looks like a choice
▸ Credits: 0.06 • Time: 30s
────────────────────────────────────────
kiro_default · auto · ◔ 1%                        /work
›  ask a question or describe a task ↵
                                                   /copy to clipboard
`

	kiroBusyPane = `  The transformative moment came around 1440 in Mainz.
────────────────────────────────────────
kiro_default · auto · ◔ 1%                        /work
›  Kiro is working · 5s · Type to steer · Ctrl+S to queue
`
)

func TestDetectKiroApproval(t *testing.T) {
	q := DetectKiro(kiroApprovalPane)
	if q == nil {
		t.Fatal("Kiro's approval menu was not recognised")
	}
	if q.Title != "Shell" || q.Prompt != "shell requires approval" || q.Detail != "Shell ls -la" {
		t.Errorf("got title %q prompt %q detail %q", q.Title, q.Prompt, q.Detail)
	}
	want := []string{"Yes, single permission", "Trust, always allow in this session", "No"}
	if len(q.Options) != len(want) {
		t.Fatalf("got %d options, want %d: %+v", len(q.Options), len(want), q.Options)
	}
	for i, label := range want {
		if q.Options[i].Label != label || q.Options[i].Key != string(rune('1'+i)) {
			t.Errorf("option %d = %+v, want %q keyed %d", i, q.Options[i], label, i+1)
		}
	}
	if q.FocusIndex != 0 || !q.Options[0].Selected {
		t.Errorf("focus = %d, want the first option", q.FocusIndex)
	}
}

func TestDetectKiroFollowsTheCursor(t *testing.T) {
	moved := `
 shell requires approval
   Yes, single permission
 ❯ Trust, always allow in this session
   No (Tab to edit)
────────────────────────────────────────
 esc to close · ↑↓ to navigate · ↵ to select · Tab to edit
`
	if q := DetectKiro(moved); q == nil || q.FocusIndex != 1 {
		t.Fatalf("focus after moving down = %+v", q)
	}
}

// A numbered or bulleted list in Kiro's own reply must never be offered as an
// answerable menu: answering it would press keys into the normal prompt.
func TestDetectKiroIgnoresTheConversation(t *testing.T) {
	for name, pane := range map[string]string{"idle": kiroIdlePane, "busy": kiroBusyPane} {
		if q := DetectKiro(pane); q != nil {
			t.Errorf("%s pane read as a question: %+v", name, q)
		}
	}
	// Two cursors means the screen is mid-redraw, not a state to act on.
	doubled := `
 shell requires approval
 ❯ Yes, single permission
 ❯ No (Tab to edit)
 esc to close · ↑↓ to navigate · ↵ to select · Tab to edit
`
	if q := DetectKiro(doubled); q != nil {
		t.Errorf("a menu with two cursors was accepted: %+v", q)
	}
}

// Kiro redraws the footer for the focused row. On "Trust" it offers a
// sub-menu instead of a selection, and a detector keyed on one wording lost
// the menu the moment the cursor moved onto it — found by answering a live
// session, where the adapter then rightly refused to press Enter blind.
func TestDetectKiroSurvivesItsFooterChanging(t *testing.T) {
	trustFocused := `↓ Shell ls -la
────────────────────────────────────────
 shell requires approval
   Yes, single permission
 ❯ Trust, always allow in this session
   No (Tab to edit)
────────────────────────────────────────
 esc to close · Enter to see more options
`
	q := DetectKiro(trustFocused)
	if q == nil || q.FocusIndex != 1 || len(q.Options) != 3 {
		t.Fatalf("got %+v", q)
	}
	// Same decision as with the cursor on the first row, so the same id: the
	// phone's answer must still apply after focus moves.
	if first := DetectKiro(kiroApprovalPane); first.Prompt != q.Prompt || first.Detail != q.Detail {
		t.Errorf("moving focus changed the question: %+v vs %+v", first, q)
	}
}

// Choosing Trust opens a second menu: how widely to trust the tool. Its rows
// have two columns, the scope and the pattern that scope would allow.
func TestDetectKiroTrustOptions(t *testing.T) {
	pane := `↓ Shell ls -la
────────────────────────────────────────
 shell requires approval · trust options
 ❯ Full command    ls -la
   Base command    ls *
   Entire tool
────────────────────────────────────────
 esc to close · ↑↓ to navigate · ↵ to select · Tab to edit
`
	q := DetectKiro(pane)
	if q == nil {
		t.Fatal("the trust options menu was not recognised")
	}
	if q.Prompt != "shell requires approval · trust options" || q.Detail != "Shell ls -la" {
		t.Errorf("prompt %q detail %q", q.Prompt, q.Detail)
	}
	want := []Option{
		{Key: "1", Label: "Full command", Description: "ls -la", Selected: true},
		{Key: "2", Label: "Base command", Description: "ls *"},
		{Key: "3", Label: "Entire tool"},
	}
	if len(q.Options) != len(want) {
		t.Fatalf("options = %+v", q.Options)
	}
	for i := range want {
		if q.Options[i] != want[i] {
			t.Errorf("option %d = %+v, want %+v", i, q.Options[i], want[i])
		}
	}
	// A different decision from the first level, so a different question: an
	// answer meant for one must never apply to the other.
	if first := DetectKiro(kiroApprovalPane); first.Prompt == q.Prompt {
		t.Error("the two levels read as the same question")
	}
}
