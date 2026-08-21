package question

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Captured verbatim from a real Claude Code session sitting on a permission
// prompt. The whole feature rests on parsing this shape correctly, so the
// fixture is the real thing rather than something idealized.
const claudePermission = ` I'll run that for you.

  Running 1 shell command…
  ⎿  $ curl -s https://example.com | head -3

────────────────────────────────────────────────────────────────────────
 Bash command

   curl -s https://example.com | head -3
   Fetch example.com and show first 3 lines

 This command requires approval

 Do you want to proceed?
 ❯ 1. Yes
   2. Yes, and don’t ask again for: curl -s https://example.com
   3. No

 Esc to cancel · Tab to amend · ctrl+e to explain`

func TestDetectsClaudePermissionPrompt(t *testing.T) {
	q := Detect(claudePermission)
	if q == nil {
		t.Fatal("no question detected in a pane that is plainly blocked on one")
	}

	if q.Prompt != "Do you want to proceed?" {
		t.Errorf("Prompt = %q", q.Prompt)
	}
	if q.Title != "Bash command" {
		t.Errorf("Title = %q, want the heading above the detail", q.Title)
	}
	if len(q.Options) != 3 {
		t.Fatalf("got %d options, want 3: %+v", len(q.Options), q.Options)
	}
	if q.Options[0].Key != "1" || q.Options[0].Label != "Yes" {
		t.Errorf("first option = %+v", q.Options[0])
	}
	if !q.Options[0].Selected {
		t.Error("the ❯ marker should mark the highlighted option")
	}
	if q.Options[2].Label != "No" {
		t.Errorf("last option = %+v", q.Options[2])
	}
	// The command being approved is the single most important thing to show
	// on a phone — approving blind is worse than not approving at all.
	if q.Detail == "" || !contains(q.Detail, "curl -s https://example.com") {
		t.Errorf("Detail = %q, want the command under review", q.Detail)
	}
}

// Captured verbatim from Claude Code 2.1.220. AskUserQuestion inserts two rows
// that are not actual listed answers: "Type something" opens an input, while
// "Chat about this" starts a conversation. It also puts a rule between them.
// The old parser stopped at that interior rule, saw only option 4, and hid the
// question until Claude's final review screen. The fixture is the complete
// capture-pane frame supplied in question-brief/panes/frame_01.txt.
func TestDetectsClaudeAskUserQuestionBeforeReview(t *testing.T) {
	q := Detect(readFixture(t, "testdata/claude_single_real_pane.txt"))
	if q == nil {
		t.Fatal("Claude's active question disappeared until the final review screen")
	}
	if q.Prompt != "Pick either one — this is only to capture what the pane looks like while a question is showing." {
		t.Errorf("Prompt = %q", q.Prompt)
	}
	if q.Title != "Capture" {
		t.Errorf("Title = %q, want checkbox chrome removed", q.Title)
	}
	if len(q.Options) != 2 {
		t.Fatalf("got %d real options, want 2: %+v", len(q.Options), q.Options)
	}
	if q.Options[0].Label != "Alpha" || !q.Options[0].Selected {
		t.Errorf("first option = %+v", q.Options[0])
	}
	if q.Options[1].Label != "Beta" {
		t.Errorf("second option = %+v", q.Options[1])
	}
	if q.Options[0].Description != "First option." ||
		q.Options[1].Description != "Second option." {
		t.Errorf("option subtext was lost: %+v", q.Options)
	}
	if !q.Custom || q.CustomKey != "3" {
		t.Errorf("custom input metadata was lost: %+v", q)
	}
	if q.Multiple {
		t.Error("a normal AskUserQuestion menu was marked multi-select")
	}
}

func TestRealClaudePaneStopsBeingAQuestionAfterAnswer(t *testing.T) {
	if q := Detect(readFixture(t, "testdata/claude_answered_real_pane.txt")); q != nil {
		t.Errorf("post-answer pane was treated as a live question: %+v", q)
	}
}

// Set AGENTMAN_CLAUDE_PANE_CORPUS to the question-brief/panes directory to
// replay every raw capture supplied by a live Claude session. The two compact
// fixtures above keep CI hermetic; this corpus check makes field diagnostics
// reproducible without checking dozens of post-answer transcript frames in.
func TestClaudePaneCorpus(t *testing.T) {
	dir := os.Getenv("AGENTMAN_CLAUDE_PANE_CORPUS")
	if dir == "" {
		t.Skip("live Claude pane corpus not available")
	}
	files, err := filepath.Glob(filepath.Join(dir, "frame_*.txt"))
	if err != nil || len(files) == 0 {
		t.Fatalf("pane corpus: files=%d err=%v", len(files), err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		q := Detect(string(raw))
		if filepath.Base(file) == "frame_01.txt" {
			if q == nil || len(q.Options) != 2 || q.Options[0].Description != "First option." {
				t.Errorf("live question frame parsed incorrectly: %+v", q)
			}
		} else if q != nil {
			t.Errorf("%s is post-answer activity but parsed as a question: %+v", file, q)
		}
	}
}

// Set AGENTMAN_CLAUDE_MULTI_PANE_CORPUS to the addendum's
// panes-multiselect directory. Frames 1-24 are real redraws of the same live
// checkbox form (including typed custom text and Next focus), frame 25 is its
// real review page, and the remainder are post-answer transcript.
func TestClaudeMultiPaneCorpus(t *testing.T) {
	dir := os.Getenv("AGENTMAN_CLAUDE_MULTI_PANE_CORPUS")
	if dir == "" {
		t.Skip("live Claude multi-select pane corpus not available")
	}
	files, err := filepath.Glob(filepath.Join(dir, "multi_*.txt"))
	if err != nil || len(files) != 27 {
		t.Fatalf("multi-select pane corpus: files=%d err=%v", len(files), err)
	}
	for index, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		q := Detect(string(raw))
		frame := index + 1
		switch {
		case frame <= 24:
			if q == nil {
				t.Errorf("%s: live multi-select form was not detected", file)
				continue
			}
			if !q.Multiple || !q.Custom || q.CustomKey != "4" ||
				q.ChoiceCount != 4 || len(q.Options) != 3 {
				t.Errorf("%s: form metadata parsed incorrectly: %+v", file, q)
				continue
			}
			if q.Title != "" || q.Options[0].Label != "Alpha" ||
				q.Options[0].Description != "Subtext for alpha." ||
				q.Options[2].Description != "Subtext for charlie." {
				t.Errorf("%s: labels/subtext parsed incorrectly: %+v", file, q)
			}
			if frame == 4 && !q.SubmitFocused {
				t.Errorf("%s: focused Next control was reported as option 1", file)
			}
		case frame == 25:
			if q == nil || !isReviewConfirmation(q) {
				t.Errorf("%s: live review was not detected: %+v", file, q)
			}
		default:
			if q != nil {
				t.Errorf("%s: post-answer transcript parsed as live: %+v", file, q)
			}
		}
	}
}

// Set AGENTMAN_CLAUDE_PREVIEW_PANE_CORPUS to the addendum's panes-preview
// directory. All eleven frames are real redraws as the preview note grows and
// the side-by-side panel shifts left.
func TestClaudePreviewPaneCorpus(t *testing.T) {
	dir := os.Getenv("AGENTMAN_CLAUDE_PREVIEW_PANE_CORPUS")
	if dir == "" {
		t.Skip("live Claude preview pane corpus not available")
	}
	files, err := filepath.Glob(filepath.Join(dir, "preview_*.txt"))
	if err != nil || len(files) != 11 {
		t.Fatalf("preview pane corpus: files=%d err=%v", len(files), err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		q := Detect(string(raw))
		if q == nil {
			t.Errorf("%s: live preview form was not detected", file)
			continue
		}
		if q.Title != "" || q.Prompt != "Preview layout — capturing the side-by-side rendering." ||
			len(q.Options) != 2 || q.Options[0].Label != "First" || q.Options[1].Label != "Second" {
			t.Errorf("%s: preview form parsed incorrectly: %+v", file, q)
		}
		for _, option := range q.Options {
			if strings.ContainsAny(option.Label+option.Description, "┌│└") ||
				strings.Contains(option.Description, "PREVIEW PANEL") {
				t.Errorf("%s: preview panel leaked into option text: %+v", file, q.Options)
			}
		}
	}
}

func TestDetectsClaudeMultiSelectAndCheckboxState(t *testing.T) {
	const pane = `────────────────────────────────────────────────────────────────────────
 ☐ Actions

Which actions should I take?

❯ 1.[ ]Interrupt a run
  Stop an active agent.
  2.[✓]Approve a command
  Allow the pending command.
  3.[ ]Re-run the tests
  4.[×]Kill a stale session
  5. [ ] Type something

  Next

Enter to select · ↑/↓ to navigate · Esc to cancel`

	q := Detect(pane)
	if q == nil {
		t.Fatal("Claude's multi-select question was not detected")
	}
	if !q.Multiple || !q.Custom {
		t.Fatalf("question capabilities were lost: %+v", q)
	}
	if len(q.Options) != 4 {
		t.Fatalf("got %d real options, want 4: %+v", len(q.Options), q.Options)
	}
	if q.ChoiceCount != 5 || q.CustomIndex != 4 || q.FocusIndex != 0 {
		t.Errorf("terminal layout metadata = count %d custom %d focus %d",
			q.ChoiceCount, q.CustomIndex, q.FocusIndex)
	}
	if q.Options[0].Checked || !q.Options[1].Checked || !q.Options[3].Checked {
		t.Errorf("checkbox state parsed incorrectly: %+v", q.Options)
	}
	if q.Options[0].Description != "Stop an active agent." ||
		q.Options[1].Description != "Allow the pending command." {
		t.Errorf("multi-select subtext was lost: %+v", q.Options)
	}
}

func TestDetectsClaudeMultiSelectWithSubmitFocused(t *testing.T) {
	const pane = `Which targets should be included?
  1.[✓]API
  2.[ ]CLI
  3. [ ] Type something

❯ Submit

Enter to select · ↑/↓ to navigate · Esc to cancel`

	q := Detect(pane)
	if q == nil {
		t.Fatal("question disappeared when focus moved to Submit")
	}
	if !q.SubmitFocused || q.FocusIndex != -1 {
		t.Errorf("focus metadata = submit %t option %d", q.SubmitFocused, q.FocusIndex)
	}
	if len(q.Options) != 2 || !q.Options[0].Checked {
		t.Errorf("options = %+v", q.Options)
	}
}

func TestDetectsRealClaudeMultiSelectWithRetainedOptionMarker(t *testing.T) {
	q := Detect(readFixture(t, "testdata/claude_multiselect_submit_real_pane.txt"))
	if q == nil {
		t.Fatal("real multi-select frame disappeared while Next was focused")
	}
	if !q.Multiple || !q.Custom || q.CustomKey != "4" ||
		q.ChoiceCount != 4 || q.CustomIndex != 3 {
		t.Fatalf("real form layout metadata was lost: %+v", q)
	}
	if !q.SubmitFocused || q.FocusIndex != -1 {
		t.Errorf("Next focus was mistaken for the retained option marker: %+v", q)
	}
	if len(q.Options) != 3 || q.Options[0].Label != "Alpha" ||
		q.Options[0].Description != "Subtext for alpha." || !q.Options[0].Checked {
		t.Errorf("real checkbox labels/subtext/state parsed incorrectly: %+v", q.Options)
	}
	for _, option := range q.Options {
		if option.Selected {
			t.Errorf("an option was selected while Next had focus: %+v", q.Options)
		}
	}
}

func TestDetectsRealClaudeMultiSelectAfterCustomPlaceholderIsReplaced(t *testing.T) {
	q := Detect(readFixture(t, "testdata/claude_multiselect_typed_real_pane.txt"))
	if q == nil {
		t.Fatal("real multi-select frame disappeared after custom text was typed")
	}
	if !q.Custom || q.CustomKey != "4" || !q.CustomChecked ||
		q.CustomIndex != 3 || q.FocusIndex != 3 || q.ChoiceCount != 4 {
		t.Fatalf("typed custom row was mistaken for a listed option: %+v", q)
	}
	if len(q.Options) != 3 {
		t.Errorf("typed custom text leaked into phone options: %+v", q.Options)
	}
}

func TestDetectsCompleteWrappedClaudeQuestion(t *testing.T) {
	q := Detect(readFixture(t, "testdata/claude_wrapped_question_real_pane.txt"))
	if q == nil {
		t.Fatal("real wrapped Claude question was not detected")
	}
	const want = "Multi-select — select some, then submit. Does it advance, or toggle option one?"
	if q.Prompt != want {
		t.Errorf("Prompt = %q, want %q", q.Prompt, want)
	}
	if strings.Contains(q.Detail, "Multi-select — select some") {
		t.Errorf("the first physical question line leaked into Detail: %q", q.Detail)
	}
	if q.Title != "" {
		t.Errorf("Title = %q, want ambiguous multi-question tabs omitted", q.Title)
	}
}

func TestDetectsRealClaudeSideBySidePreview(t *testing.T) {
	q := Detect(readFixture(t, "testdata/claude_preview_real_pane.txt"))
	if q == nil {
		t.Fatal("real side-by-side preview frame did not produce a question")
	}
	if q.Prompt != "Preview layout — capturing the side-by-side rendering." ||
		len(q.Options) != 2 || q.Options[0].Label != "First" || q.Options[1].Label != "Second" {
		t.Fatalf("real preview frame parsed incorrectly: %+v", q)
	}
	if !q.AdvanceWithTab {
		t.Error("wrapped 'Tab to switch questions' hint was not retained as navigation metadata")
	}
	if q.Options[0].Preview != "PREVIEW PANEL ONE\n\nthis panel renders to the\nright of the option list,\nwhich narrows the option\ncolumn considerably" {
		t.Errorf("visible preview panel was not preserved: %q", q.Options[0].Preview)
	}
	if q.Options[1].Preview != "" {
		t.Errorf("the pane only shows the highlighted option's preview: %+v", q.Options)
	}
	for _, option := range q.Options {
		if strings.ContainsAny(option.Label+option.Description, "┌│└") ||
			strings.Contains(option.Description, "PREVIEW PANEL") {
			t.Errorf("preview panel leaked into option text: %+v", q.Options)
		}
	}
}

func TestDetectsClaudeFinalReviewWithoutFooter(t *testing.T) {
	const pane = `────────────────────────────────────────────────────────────────────────
 Questions 3/3
 Review your answers

Which database should we use?
  → Postgres
Which targets should be included?
  → API, CLI
Where should this be deployed?
  → Dublin

Ready to submit your answers?

❯ 1. Submit answers
  2. Cancel`

	q := Detect(pane)
	if q == nil {
		t.Fatal("Claude's final review page was not detected")
	}
	if q.Title != "Review your answers" || q.Prompt != "Ready to submit your answers?" {
		t.Errorf("review heading = %q / %q", q.Title, q.Prompt)
	}
	if len(q.Options) != 2 || q.Options[0].Label != "Submit answers" ||
		q.Options[1].Label != "Cancel" {
		t.Errorf("review options = %+v", q.Options)
	}
	for _, want := range []string{"Which database", "Postgres", "API, CLI", "Dublin"} {
		if !contains(q.Detail, want) {
			t.Errorf("Detail = %q, want review content %q", q.Detail, want)
		}
	}
}

func TestDetectsRealClaudeIncompleteReviewDetail(t *testing.T) {
	q := Detect(readFixture(t, "testdata/claude_incomplete_review_real_pane.txt"))
	if q == nil {
		t.Fatal("real incomplete Claude review screen was not detected")
	}
	if q.Title != "Review your answers" || q.Prompt != "Ready to submit your answers?" {
		t.Fatalf("review heading = %q / %q", q.Title, q.Prompt)
	}
	for _, want := range []string{
		"⚠ You have not answered all questions",
		"● Did Codex advance exactly once",
		"→ Yes",
	} {
		if !contains(q.Detail, want) {
			t.Errorf("Detail = %q, want live review content %q", q.Detail, want)
		}
	}
}

func TestReviewLabelsAloneDoNotBypassLiveMenuGuard(t *testing.T) {
	const prose = `A future screen might contain:
❯ 1. Submit answers
  2. Cancel`
	if q := Detect(prose); q != nil {
		t.Errorf("review-like prose without the exact review context was detected: %+v", q)
	}
}

func TestDetectsClaudeReviewWhenHeadingScrolledOffPane(t *testing.T) {
	const pane = `A long fourth answer wraps across the visible review pane.
The heading is now above the captured viewport.

Ready to submit your answers?

❯ 1. Submit answers
  2. Cancel`
	q := Detect(pane)
	if q == nil {
		t.Fatal("review confirmation disappeared when its heading scrolled off screen")
	}
	if q.Title != "Review your answers" {
		t.Errorf("Title = %q", q.Title)
	}
}

func TestNoQuestionWhenAgentIsWorking(t *testing.T) {
	const working = `⏺ I'll run that for you.

  Listed 1 directory

⏺ The directory is empty — just . and .., no files.

✻ Crunched for 5s
────────────────────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────────────────────
  mac@host  ~/code  Opus 5  ctx:97%`

	if q := Detect(working); q != nil {
		t.Errorf("detected a question in an idle pane: %+v", q)
	}
}

func TestIgnoresProseThatLooksLikeAMenu(t *testing.T) {
	// A single numbered line is ordinary writing, not a choice.
	const prose = `⏺ Here is the plan:

   1. Install the dependencies

 Then we can continue.`
	if q := Detect(prose); q != nil {
		t.Errorf("a lone numbered line was treated as a menu: %+v", q)
	}
}

func TestIgnoresNumberedListAtBottomOfPane(t *testing.T) {
	const prose = `Here are your options:

  1. Keep the current implementation
  2. Rewrite it later`
	if q := Detect(prose); q != nil {
		t.Errorf("an ordinary numbered list was treated as a live menu: %+v", q)
	}
}

func TestIgnoresAnAlreadyAnsweredMenu(t *testing.T) {
	// The menu scrolled past and the agent carried on; nothing is pending.
	const answered = ` Do you want to proceed?
 ❯ 1. Yes
   2. No

⏺ Running the command now.

  Fetched example.com`
	if q := Detect(answered); q != nil {
		t.Errorf("detected a resolved menu as pending: %+v", q)
	}
}

func TestDetectsCodexStyleApproval(t *testing.T) {
	// Codex renders the same shape with different wording.
	const codex = `────────────────────────────────────────────────────────────────────────
 Shell command

   rm -rf ./build

 Allow this command?
   1. Yes, run it
 ❯ 2. No, tell Codex what to do differently

 Press Esc to cancel`

	q := Detect(codex)
	if q == nil {
		t.Fatal("no question detected")
	}
	if q.Prompt != "Allow this command?" {
		t.Errorf("Prompt = %q", q.Prompt)
	}
	if len(q.Options) != 2 {
		t.Fatalf("got %d options, want 2", len(q.Options))
	}
	if !q.Options[1].Selected {
		t.Error("the second option is highlighted and should be marked so")
	}
}

// Captured from a live Codex session. Two things here only showed up against
// the real terminal: Codex marks its selection with › (U+203A) rather than
// Claude's ❯, and it pads a description onto the same line as the choice.
const codexLive = `■ You've hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), visit
https://chatgpt.com/codex/settings/usage to purchase more credits or try again at Aug 16th, 2026 4:19 AM.
  Approaching rate limits
  Switch to gpt-5.6-luna for lower credit usage?
› 1. Switch to gpt-5.6-luna                 Fast and affordable agentic coding model.
  2. Keep current model
  3. Keep current model (never show again)  Hide future rate limit reminders about switching models.
  Press enter to confirm or esc to go back`

func TestDetectsCodexSelectionMarker(t *testing.T) {
	q := Detect(codexLive)
	if q == nil {
		t.Fatal("no question detected in a live Codex menu")
	}
	if len(q.Options) != 3 {
		t.Fatalf("got %d options, want 3 — an unmatched marker turns a choice "+
			"into the question: %+v", len(q.Options), q.Options)
	}
	if !q.Options[0].Selected {
		t.Error("› should mark the highlighted option")
	}
	if q.Prompt != "Switch to gpt-5.6-luna for lower credit usage?" {
		t.Errorf("Prompt = %q", q.Prompt)
	}
	// A choice's trailing description must not fuse into its label; the label
	// has to read as a button.
	if q.Options[2].Label != "Keep current model (never show again)" {
		t.Errorf("option 3 label = %q, want the choice without its description",
			q.Options[2].Label)
	}
	if q.Options[2].Description != "Hide future rate limit reminders about switching models." {
		t.Errorf("option 3 description = %q", q.Options[2].Description)
	}
	// Codex draws no rule around its prompt, so there is no heading to claim.
	if q.Title != "" {
		t.Errorf("Title = %q, want empty for an undelimited prompt", q.Title)
	}
}

// Codex 0.147 renders request_user_input as a multi-question overlay. At the
// real session's 68-column width, descriptions wrap into a second column and
// the footer wraps into multiple hint lines. This is the redraw shown after
// question 1 is answered and the form advances to question 2.
const codexSecondQuestion = `  Question 2/3 (2 unanswered)
  How would you like me to collaborate with you?
  › 1. Take the lead (Recommended)  I’ll make reasonable decisions and
                                    bring you in for important tradeoffs.
    2. Work step by step            We’ll make each significant decision
                                    together.
    3. Advise only                  I’ll provide guidance while leaving all
                                    actions to you.

  enter to submit answer | ←/→ to navigate questions
  esc to interrupt`

func TestDetectsCodexQuestionAfterFormAdvances(t *testing.T) {
	q := Detect(codexSecondQuestion)
	if q == nil {
		t.Fatal("Codex's second question was lost after the first answer")
	}
	if q.Prompt != "How would you like me to collaborate with you?" {
		t.Errorf("Prompt = %q", q.Prompt)
	}
	if q.Detail != "" {
		t.Errorf("Detail = %q, want progress chrome excluded", q.Detail)
	}
	if len(q.Options) != 3 {
		t.Fatalf("got %d options, want 3: %+v", len(q.Options), q.Options)
	}
	want := []string{"Take the lead (Recommended)", "Work step by step", "Advise only"}
	for i, label := range want {
		if q.Options[i].Label != label {
			t.Errorf("option %d label = %q, want %q", i+1, q.Options[i].Label, label)
		}
	}
	if !q.Options[0].Selected {
		t.Error("Codex's › marker should preserve the current selection")
	}
	if !contains(q.Options[0].Description, "make reasonable decisions") ||
		!contains(q.Options[0].Description, "important tradeoffs") {
		t.Errorf("wrapped description = %q", q.Options[0].Description)
	}
}

func TestLiveMenuRequiresFooter(t *testing.T) {
	const unfinishedTranscript = `A suggested sequence:
› 1. First choice
  2. Second choice`
	if q := Detect(unfinishedTranscript); q != nil {
		t.Errorf("numbered transcript without live-menu hints was detected: %+v", q)
	}
}

func TestHandlesEmptyAndGarbage(t *testing.T) {
	for _, pane := range []string{"", "\n\n\n", "no menu here at all"} {
		if q := Detect(pane); q != nil {
			t.Errorf("Detect(%q) = %+v, want nil", pane, q)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}()
}

func readFixture(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Captured verbatim while the app reported the session as idle and showed no
// card, with the question plainly on the terminal.
//
// Claude Code paints its task list beneath the live control. The guard that
// rejects anything unrecognised after the options — there to stop a numbered
// list in an assistant reply becoming remotely answerable — treated those rows
// as proof the menu had been answered and scrolled past, so Detect returned
// nil and every question went invisible to the phone. It only began once a
// task list existed, which is why it looked like a sudden regression.
func TestDetectsAQuestionWithATaskListBelowIt(t *testing.T) {
	q := Detect(readFixture(t, "testdata/claude_task_list_below_real_pane.txt"))
	if q == nil {
		t.Fatal("the task list hid a live question from the app")
	}
	if q.Title != "Android next" {
		t.Errorf("Title = %q", q.Title)
	}
	if q.Prompt != "The Android AAB is building. Once it finishes, what should happen with it?" {
		t.Errorf("Prompt = %q", q.Prompt)
	}
	// The three real answers. "Type something" and "Chat about this" are rows
	// AskUserQuestion always adds and are not listed answers; the first becomes
	// Custom, the second is dropped.
	if len(q.Options) != 3 {
		t.Fatalf("Options = %d, want 3", len(q.Options))
	}
	if q.Options[1].Label != "Submit to Play internal testing" {
		t.Errorf("Options[1].Label = %q", q.Options[1].Label)
	}
	if !q.Custom {
		t.Error("Custom = false, want true — the pane offers a free-text row")
	}
}
