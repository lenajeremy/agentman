package question

import (
	"os"
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

// Panes captured from agy 1.2.17 in a 100-column tmux pane, paths shortened.
func agyPane(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/antigravity_" + name + "_real_pane.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func labels(options []Option) []string {
	out := make([]string, len(options))
	for i, option := range options {
		out[i] = option.Label
	}
	return out
}

// Each of agy's permission prompts names the decision above its rule and puts
// the subject — a command, a diff, a path, a URL — between the rule and the
// question. The phone shows them as title, detail and prompt.
func TestDetectAntigravityReadsEveryPermissionPrompt(t *testing.T) {
	for _, tc := range []struct {
		pane, title, detail, prompt string
		options                     []string
		amend                       bool
	}{
		{"command", "Command", "sleep 3; echo agentman-research", "Run this command?", []string{
			"Yes, run command",
			"Yes, and always allow in this conversation for commands that start with 'sleep'",
			"Yes, and always allow for commands that start with 'sleep' (Persist to settings.json)",
			"No, cancel",
		}, true},
		{"command_with_review", "Command", "ls", "Run this command?", []string{
			"Yes, run command",
			"Yes, and always allow in this conversation for commands that start with 'ls'",
			"Yes, and always allow for commands that start with 'ls' (Persist to settings.json)",
			"No, cancel",
		}, true},
		{"create_file", "Create file", "/work/ws1/notes.txt  +1\n1 +  hello", "Allow creation of this file?",
			[]string{"Yes, allow creation", "No, deny creation"}, true},
		// The key hint between the diff and the question is neither.
		{"edit_file", "Pending edit", "/work/ws1/notes.txt  +1 -1\n1 -  hello\n1 +  hello world", "Accept this file edit?",
			[]string{"Yes, accept this change", "No, reject this change"}, true},
		{"file_access", "File access", "Read: /work/upload/att.png\nReason: outside workspace", "Allow access to this file?",
			[]string{"Yes, allow access", "Yes, and always allow non-workspace access", "No, deny access"}, false},
		{"url", "Read URL", "https://example.com", "Allow access to this URL?", []string{
			"Yes, allow access",
			"Yes, and always allow access to 'example.com' in this conversation",
			"Yes, and always allow access to 'example.com' (Persist to settings.json)",
			"No, deny access",
			"No, and always deny access to 'example.com' in this conversation",
			"No, and always deny access to 'example.com' (Persist to settings.json)",
		}, true},
	} {
		q, form := DetectAntigravityForm(agyPane(t, tc.pane))
		if q == nil {
			t.Errorf("%s: not recognised", tc.pane)
			continue
		}
		if q.Title != tc.title || q.Detail != tc.detail || q.Prompt != tc.prompt {
			t.Errorf("%s: title %q detail %q prompt %q", tc.pane, q.Title, q.Detail, q.Prompt)
		}
		if got := labels(q.Options); strings.Join(got, "|") != strings.Join(tc.options, "|") {
			t.Errorf("%s: options %q", tc.pane, got)
		}
		if form.Kind != "approval" || form.Amend != tc.amend || form.Typing || q.FocusIndex != 0 || q.Custom || q.Multiple {
			t.Errorf("%s: form %+v focus %d custom %v multiple %v", tc.pane, form, q.FocusIndex, q.Custom, q.Multiple)
		}
	}
}

// Amend turns the first option into a message box. Keys pressed now are typed
// into it, so the form must say so before anything answers it.
func TestDetectAntigravitySeesTheAmendBoxOpen(t *testing.T) {
	q, form := DetectAntigravityForm(agyPane(t, "url_amend_typing"))
	if q == nil || !form.Typing || q.Options[0].Label != "Yes, and tell Antigravity CLI what to do next" ||
		len(q.Options) != 6 {
		t.Errorf("question %+v form %+v", q, form)
	}
}

// The agent's own questions: "Write-in..." opens a text box rather than being
// an answer, so it is the free-text option, not a choice.
func TestDetectAntigravityQuestionForm(t *testing.T) {
	q, form := DetectAntigravityForm(agyPane(t, "question"))
	if q == nil || q.Title != "Question" || q.Prompt != "Which colour do you prefer?" || q.Detail != "" {
		t.Fatalf("question %+v", q)
	}
	if strings.Join(labels(q.Options), "|") != "Red|Green|Blue" || !q.Custom || q.Multiple {
		t.Errorf("options %q custom %v multiple %v", labels(q.Options), q.Custom, q.Multiple)
	}
	if form != (AntigravityForm{Kind: "question", Index: 1, Count: 1, WriteInKey: "4"}) {
		t.Errorf("form %+v", form)
	}

	typing, form := DetectAntigravityForm(agyPane(t, "question_writein_typing"))
	if typing == nil || !form.Typing || typing.FocusIndex != 3 {
		t.Errorf("with the write-in box open: %+v %+v", typing, form)
	}
}

func TestDetectAntigravityMultiSelectQuestion(t *testing.T) {
	q, form := DetectAntigravityForm(agyPane(t, "question_multi"))
	if q == nil || q.Title != "Question 1/2" || q.Prompt != "Which fruits do you like?" || !q.Multiple || !q.Custom {
		t.Fatalf("question %+v", q)
	}
	if strings.Join(labels(q.Options), "|") != "apple|banana|cherry" || form.Index != 1 || form.Count != 2 {
		t.Errorf("options %q form %+v", labels(q.Options), form)
	}
	checked, _ := DetectAntigravityForm(agyPane(t, "question_multi_checked"))
	if checked == nil || !checked.Options[0].Checked || checked.Options[1].Checked || !checked.Options[2].Checked ||
		checked.FocusIndex != 2 {
		t.Errorf("checked %+v", checked)
	}

	last, form := DetectAntigravityForm(agyPane(t, "question_last"))
	if last == nil || last.Title != "Question 2/2" || last.Prompt != "Which size?" || last.Multiple ||
		form.Index != 2 || form.Count != 2 || form.WriteInKey != "3" {
		t.Errorf("last question %+v form %+v", last, form)
	}
}

// A subagent's permission request is drawn over the parent's prompt while the
// parent itself sits idle. It is a decision like any other.
func TestDetectAntigravitySubagentApproval(t *testing.T) {
	q, form := DetectAntigravityForm(agyPane(t, "subagent_approval"))
	if q == nil || form.Kind != "subagent" {
		t.Fatalf("not recognised: %+v %+v", q, form)
	}
	if q.Title != "Subagent research" || q.Prompt != "research needs approval for Read" ||
		q.Detail != "Read(~/.zsh_history)" || strings.Join(labels(q.Options), "|") != "Yes, approve|No, deny" {
		t.Errorf("question %+v", q)
	}
}

// Panels and pickers carry their own key hints but nothing numbered: none of
// them is a decision to answer from the phone, and nor is a finished reply.
func TestDetectAntigravityIgnoresPanelsAndReplies(t *testing.T) {
	for _, pane := range []string{"idle_after_reply", "artifact_panel", "model_picker", "help_panel"} {
		if q, form := DetectAntigravityForm(agyPane(t, pane)); q != nil {
			t.Errorf("%s read as a question: %+v %+v", pane, q, form)
		}
	}
	if q, form := DetectAntigravityForm(agyPane(t, "trust")); q == nil || form.Kind != "trust" ||
		q.Detail != "/work/ws1" {
		t.Errorf("trust: %+v %+v", q, form)
	}
}

// The panel a subagent's request is answered in: open with the request, then
// still open after it was answered, until it is dismissed.
func TestAntigravitySubagentPanel(t *testing.T) {
	if tool, open := AntigravitySubagentPanel(agyPane(t, "subagent_panel")); !open || tool != "Read" {
		t.Errorf("pending: tool %q open %v", tool, open)
	}
	if tool, open := AntigravitySubagentPanel(agyPane(t, "subagent_panel_answered")); !open || tool != "" {
		t.Errorf("answered: tool %q open %v", tool, open)
	}
	if _, open := AntigravitySubagentPanel(agyPane(t, "subagent_approval")); open {
		t.Error("the prompt with the request box over it read as the panel")
	}
}

// Text sent while one of these is open lands in it, not in the prompt.
func TestAntigravityPanelOpen(t *testing.T) {
	for _, pane := range []string{"help_panel", "model_picker", "artifact_panel", "context_panel",
		"review_submit", "subagent_panel", "subagent_panel_answered"} {
		if !AntigravityPanelOpen(agyPane(t, pane)) {
			t.Errorf("%s: panel not seen", pane)
		}
	}
	for _, pane := range []string{"idle_after_reply", "command", "question", "subagent_approval", "trust"} {
		if AntigravityPanelOpen(agyPane(t, pane)) {
			t.Errorf("%s: read as a panel", pane)
		}
	}
}
