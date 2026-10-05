package question

import "testing"

// Screens captured from Kiro CLI 2.24.1 in a 160-column tmux pane. The long
// rules are shortened; nothing else is.
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

// Real screens from Kiro CLI 2.27.1 in an 80x24 pane, the size a phone launch
// gets. Only the scratch directory's path was replaced with /work.

// The call under review was read from the single line above the menu, and
// that line is often something else: a write's diff, a sibling call's "esc
// to cancel", a parameter row, a task-list status. Each time the phone asked
// "Shell requires approval" without saying what.
func TestDetectKiroFindsTheCallUnderReview(t *testing.T) {
	cases := []struct {
		fixture, title, prompt, detail string
	}{
		{"kiro_write_approval_real_pane.txt", "Write", "write requires approval",
			"Write /work/hello.txt\nadded 1 line in hello.txt\n1+  hi there"},
		{"kiro_edit_approval_real_pane.txt", "Write", "write requires approval",
			"Write /work/greeting.txt\nadded 1 line, removed 1 line in greeting.txt\n1-  hi there\n1+  hello there"},
		{"kiro_parallel_approval_real_pane.txt", "Shell", "shell requires approval", "Shell ls -la"},
		{"kiro_task_list_approval_real_pane.txt", "Write", "write requires approval",
			"Write /work/haiku.txt\nadded 3 lines in haiku.txt\n1+  Screen lights up at night\n" +
				"2+  Fingers scroll through endless feeds\n3+  Silence goes unheard"},
		{"kiro_web_fetch_approval_real_pane.txt", "Web fetch", "web_fetch requires approval", "WebFetch example.com"},
		{"kiro_write_trust_options_real_pane.txt", "Write", "write requires approval · trust options",
			"Write /work/greeting.txt\nadded 1 line in greeting.txt\n1+  hi there"},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			q := DetectKiro(readFixture(t, "testdata/"+tc.fixture))
			if q == nil {
				t.Fatal("no question")
			}
			if q.Title != tc.title || q.Prompt != tc.prompt || q.Detail != tc.detail {
				t.Errorf("title %q prompt %q detail %q,\nwant %q %q %q", q.Title, q.Prompt, q.Detail, tc.title, tc.prompt, tc.detail)
			}
		})
	}
}

func TestDetectKiroWriteTrustOptions(t *testing.T) {
	q := DetectKiro(readFixture(t, "testdata/kiro_write_trust_options_real_pane.txt"))
	want := []Option{
		{Key: "1", Label: "Specific paths", Description: "greeting.txt", Selected: true},
		{Key: "2", Label: "Complete directory", Description: "/work"},
		{Key: "3", Label: "Entire tool"},
	}
	if q == nil || len(q.Options) != len(want) {
		t.Fatalf("got %+v", q)
	}
	for i := range want {
		if q.Options[i] != want[i] {
			t.Errorf("option %d = %+v, want %+v", i, q.Options[i], want[i])
		}
	}
}

// Tab on "No" opens an editor for the reason. The call still waits on the
// user, so it is a question: the text box takes the reason, and its one
// choice goes back to the menu. What has been typed is not part of it, so the
// question does not change while someone types at the Mac.
func TestDetectKiroDenialReasonEditor(t *testing.T) {
	empty := DetectKiro(readFixture(t, "testdata/kiro_feedback_editor_real_pane.txt"))
	if empty == nil {
		t.Fatal("the reason editor was not recognised")
	}
	if empty.Prompt != "write requires approval · Modify request" || !empty.Custom ||
		len(empty.Options) != 1 || empty.Options[0].Key != "1" || empty.Detail != "Write /work/hello.txt\nadded 1 line in hello.txt\n1+  hi there" {
		t.Errorf("got %+v", empty)
	}
	typed := DetectKiro(readFixture(t, "testdata/kiro_feedback_typed_real_pane.txt"))
	if typed == nil || typed.Prompt != empty.Prompt || typed.Detail != empty.Detail ||
		len(typed.Options) != 1 || typed.Options[0] != empty.Options[0] {
		t.Errorf("typing changed the question: %+v", typed)
	}
	// A bare "esc to close" is also the /tools panel's footer.
	if q := DetectKiro(readFixture(t, "testdata/kiro_tools_panel_real_pane.txt")); q != nil {
		t.Errorf("the tools panel read as a question: %+v", q)
	}
}

// Pickers and panels take the keyboard from the prompt. Typed into the /model
// picker, a message becomes its search and Enter switches the model; into
// /rewind, Enter forks the session. None of them is a question, but each
// must stop a send.
func TestKiroOverlayOpen(t *testing.T) {
	open := []string{
		"kiro_slash_palette_real_pane.txt", "kiro_model_picker_real_pane.txt", "kiro_agent_picker_real_pane.txt",
		"kiro_context_panel_real_pane.txt", "kiro_tools_panel_real_pane.txt", "kiro_rewind_picker_real_pane.txt",
		"kiro_write_approval_real_pane.txt", "kiro_feedback_editor_real_pane.txt",
	}
	for _, fixture := range open {
		if !KiroOverlayOpen(readFixture(t, "testdata/"+fixture)) {
			t.Errorf("%s: overlay not seen", fixture)
		}
	}
	for _, fixture := range []string{"kiro_slash_palette_real_pane.txt", "kiro_model_picker_real_pane.txt",
		"kiro_agent_picker_real_pane.txt", "kiro_context_panel_real_pane.txt", "kiro_rewind_picker_real_pane.txt"} {
		if q := DetectKiro(readFixture(t, "testdata/"+fixture)); q != nil {
			t.Errorf("%s read as a question: %+v", fixture, q)
		}
	}
	for _, fixture := range []string{"kiro_idle_real_pane.txt", "kiro_busy_queue_mode_real_pane.txt",
		"kiro_busy_steer_queued_real_pane.txt"} {
		pane := readFixture(t, "testdata/"+fixture)
		if KiroOverlayOpen(pane) || DetectKiro(pane) != nil {
			t.Errorf("%s: the prompt has the keyboard, but an overlay or question was seen", fixture)
		}
	}
}

func TestKiroTitles(t *testing.T) {
	for tool, want := range map[string]string{
		"shell": "Shell", "web_fetch": "Web fetch", "use_aws": "AWS", "todo_list": "Todo list",
		"read_notes > shell": "Shell", "subagent": "Subagent",
	} {
		if got := kiroTitle(tool); got != want {
			t.Errorf("kiroTitle(%q) = %q, want %q", tool, got, want)
		}
	}
}
