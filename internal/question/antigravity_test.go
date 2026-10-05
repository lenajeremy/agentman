package question

import (
	"strings"
	"testing"
)

// Screens captured from Antigravity CLI 1.2.12 in a 160-column tmux pane. The
// long rules are shortened; nothing else is.
const (
	antigravityPermissionPane = `> Run the shell command: ls -la   then tell me how many files there are.
● Bash(ls -la) (ctrl+o to expand)
Command
────────────────────────────────────────
Requesting permission for:
   ls -la
Run this command?
> 1. Yes, run command
  2. Yes, and always allow in this conversation for commands that start with 'ls'
  3. Yes, and always allow for commands that start with 'ls' (Persist to settings.json)
  4. No, cancel
  ↑/↓ Navigate · tab Amend · ctrl+g edit/expand command
esc to cancel                                                     Gemini 3.8 Flash · high
`

	antigravityTrustPane = `Accessing workspace:
/Users/mac/work/probe
Do you trust the contents of this project?
Antigravity CLI requires permission to read, edit, and execute files here.
> Yes, I trust this folder
  No, exit
  ↑/↓ Navigate · enter Confirm
                                                                 Gemini 3.8 Flash · high
`

	antigravityIdlePane = `  There is 1 regular file (notes.txt).
  1. notes.txt
  2. something numbered in the reply
────────────────────────────────────────
>
────────────────────────────────────────
? for shortcuts                                                   Gemini 3.8 Flash · high
`
)

func TestDetectAntigravityPermission(t *testing.T) {
	q := DetectAntigravity(antigravityPermissionPane)
	if q == nil {
		t.Fatal("Antigravity's permission prompt was not recognised")
	}
	if q.Prompt != "Run this command?" || q.Detail != "ls -la" {
		t.Errorf("prompt %q detail %q, want the question and the command separately", q.Prompt, q.Detail)
	}
	if len(q.Options) != 4 || q.Options[0].Label != "Yes, run command" || q.Options[3].Label != "No, cancel" {
		t.Errorf("options = %+v", q.Options)
	}
	if q.FocusIndex != 0 {
		t.Errorf("focus = %d", q.FocusIndex)
	}
}

func TestDetectAntigravityTrust(t *testing.T) {
	q := DetectAntigravity(antigravityTrustPane)
	if q == nil {
		t.Fatal("Antigravity's folder trust prompt was not recognised")
	}
	if q.Detail != "/Users/mac/work/probe" || q.Title != "Workspace trust" {
		t.Errorf("title %q detail %q", q.Title, q.Detail)
	}
	if len(q.Options) != 2 || q.Options[0].Label != "Yes, I trust this folder" ||
		q.Options[1].Label != "No, exit" || q.FocusIndex != 0 {
		t.Errorf("options = %+v focus %d", q.Options, q.FocusIndex)
	}
}

func TestDetectAntigravityIgnoresTheConversation(t *testing.T) {
	if q := DetectAntigravity(antigravityIdlePane); q != nil {
		t.Errorf("idle pane read as a question: %+v", q)
	}
}

// Captured from agy in an 80-column pane, the size a phone-launched session
// starts at. agy wraps long labels itself and continues them at column 0, and
// read as-is the menu ended at the first wrap: no question was found, and the
// "esc to cancel" footer reported the session as working indefinitely.
const antigravityNarrowPermissionPane = `● Bash(ls) (ctrl+o to expand)

Command
────────────────────────────────────────────────────────────────────────────────

Requesting permission for:
   ls

Run this command?
> 1. Yes, run command
  2. Yes, and always allow in this conversation for commands that start with
'ls'
  3. Yes, and always allow for commands that start with 'ls' (Persist to
settings.json)
  4. No, cancel

  ↑/↓ Navigate · tab Amend · ctrl+g edit/expand command
esc to cancel                                            Gemini 3.8 Flash · high
`

func TestDetectAntigravityInANarrowPane(t *testing.T) {
	q := DetectAntigravity(antigravityNarrowPermissionPane)
	if q == nil {
		t.Fatal("the permission prompt went unseen at 80 columns")
	}
	want := []string{
		"Yes, run command",
		"Yes, and always allow in this conversation for commands that start with 'ls'",
		"Yes, and always allow for commands that start with 'ls' (Persist to settings.json)",
		"No, cancel",
	}
	if len(q.Options) != len(want) {
		t.Fatalf("options = %+v", q.Options)
	}
	for i, label := range want {
		if q.Options[i].Label != label {
			t.Errorf("option %d = %q, want %q", i, q.Options[i].Label, label)
		}
	}
	if q.Prompt != "Run this command?" || q.Detail != "ls" || q.FocusIndex != 0 {
		t.Errorf("prompt %q detail %q focus %d", q.Prompt, q.Detail, q.FocusIndex)
	}
	// The same decision at any width is the same question, so an answer the
	// phone prepared still applies if the pane is resized in between.
	wide := DetectAntigravity(strings.Replace(antigravityPermissionPane, "ls -la", "ls", -1))
	if wide == nil || wide.Prompt != q.Prompt || wide.Detail != q.Detail {
		t.Errorf("wide and narrow read differently: %+v vs %+v", wide, q)
	}
}

func TestDetectAntigravityTrustWithAWrappedPath(t *testing.T) {
	pane := "Accessing workspace:\n" +
		"/private/tmp/claude-501/-Users-mac-Desktop-agentman/e4820012-9179-40a6-a85c-3eb1\n" +
		"ec0124bb/scratchpad/e2e/agy\n" +
		"Do you trust the contents of this project?\n" +
		"Antigravity CLI requires permission to read, edit, and execute files here.\n" +
		"> Yes, I trust this folder\n  No, exit\n  ↑/↓ Navigate · enter Confirm\n"
	q := DetectAntigravity(pane)
	want := "/private/tmp/claude-501/-Users-mac-Desktop-agentman/e4820012-9179-40a6-a85c-3eb1ec0124bb/scratchpad/e2e/agy"
	if q == nil || q.Detail != want {
		t.Fatalf("detail = %+v, want the whole path", q)
	}
}
