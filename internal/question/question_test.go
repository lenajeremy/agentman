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
