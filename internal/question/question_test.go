package question

import "testing"

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
	// Codex draws no rule around its prompt, so there is no heading to claim.
	if q.Title != "" {
		t.Errorf("Title = %q, want empty for an undelimited prompt", q.Title)
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
