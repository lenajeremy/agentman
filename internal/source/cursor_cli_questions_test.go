package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Captures from a real Cursor CLI pane (2026.09.26, 160 columns, zen
// display), trailing spaces trimmed.
func cursorCLIPaneFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "cursor-cli-pane-"+name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func optionKeys(q *protocol.Question) []string {
	var keys []string
	for _, option := range q.Options {
		keys = append(keys, option.Key)
	}
	return keys
}

func TestCursorCLIScreensFromRealPanes(t *testing.T) {
	type want struct {
		title     string
		keys      string
		custom    bool
		customKey string
		textBox   bool
		detail    string
		busy      bool
		blocked   bool
	}
	cases := map[string]want{
		"trust":                      {title: "Workspace trust", keys: "a,q"},
		"idle-after-trust":           {},
		"busy-working":               {busy: true},
		"busy-running-tool":          {busy: true},
		"reconnecting":               {busy: true},
		"turn-error":                 {},
		"todos-done":                 {},
		"plan-mode-idle":             {},
		"multiline-draft":            {},
		"hints":                      {},
		"slash-menu":                 {},
		"text-question":              {busy: true},
		"shell-approval":             {title: "Shell command", keys: "y", custom: true, customKey: "n", detail: "echo agentman-research in ."},
		"skip-box":                   {title: "Tell the agent what to do instead", custom: true, textBox: true, detail: "echo agentman-research in .", busy: true},
		"delete-approval":            {title: "Delete file", keys: "y,n", detail: "Delete file: hello.txt"},
		"plan-approval-reconnecting": {title: "Cursor plan", keys: "b", custom: true, customKey: "p", detail: "Create goodbye.txt", busy: true},
		"plan-approval-usage-limit":  {title: "Cursor plan", keys: "b", custom: true, customKey: "p", detail: "Create goodbye.txt"},
		"plan-approval-focus-2":      {title: "Cursor plan", keys: "b", custom: true, customKey: "p", detail: "Create goodbye.txt"},
		"plan-revise-box":            {title: "Describe how to revise the plan", custom: true, textBox: true},
		"plan-rejected-revise-box":   {title: "Describe how to revise the plan", custom: true, textBox: true},
		"resume-picker":              {blocked: true},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			screen := parseCursorCLIScreen(cursorCLIPaneFixture(t, name))
			if screen.busy != want.busy {
				t.Errorf("busy = %v, want %v", screen.busy, want.busy)
			}
			if (screen.blocked != "") != want.blocked {
				t.Errorf("blocked = %q, want blocked=%v", screen.blocked, want.blocked)
			}
			q := screen.question
			if want.title == "" {
				if q != nil {
					t.Fatalf("question = %+v, want none", q)
				}
				return
			}
			if q == nil {
				t.Fatalf("no question, want %q", want.title)
			}
			if q.Title != want.title || strings.Join(optionKeys(q), ",") != want.keys || q.Custom != want.custom {
				t.Errorf("question = %q keys=%v custom=%v, want %q keys=%s custom=%v",
					q.Title, optionKeys(q), q.Custom, want.title, want.keys, want.custom)
			}
			if screen.customKey != want.customKey || screen.textBox != want.textBox {
				t.Errorf("customKey=%q textBox=%v, want %q %v", screen.customKey, screen.textBox, want.customKey, want.textBox)
			}
			if !strings.HasPrefix(q.Detail, want.detail) {
				t.Errorf("detail = %q, want it to start with %q", q.Detail, want.detail)
			}
			if q.ID == "" {
				t.Error("question has no id")
			}
		})
	}
}

// The plan the phone approves is the plan Cursor shows, not just its name.
func TestCursorCLIPlanApprovalCarriesThePlan(t *testing.T) {
	q := cursorCLIQuestionFromPane(cursorCLIPaneFixture(t, "plan-approval-focus-2"))
	if q == nil || !strings.Contains(q.Detail, "Write goodbye.txt") || !strings.Contains(q.Detail, "with contents: bye.") {
		t.Fatalf("plan detail = %+v", q)
	}
}

// Once answered, Cursor erases a menu and redraws its prompt beneath. A menu
// with a prompt under it is history and must never be answerable.
func TestCursorCLIMenuAbovePromptIsNotCurrent(t *testing.T) {
	pane := cursorCLIPaneFixture(t, "delete-approval") + "\n" +
		" ▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄\n" +
		"  → Add a follow-up\n" +
		" ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀\n" +
		"  Auto · 7.4%\n"
	if q := cursorCLIQuestionFromPane(pane); q != nil {
		t.Fatalf("stale delete menu was answerable: %+v", q)
	}
}

// A shell approval that does not show its command stays on the Mac.
func TestCursorCLIApprovalWithoutItsSubjectIsNotAnswerable(t *testing.T) {
	pane := strings.Replace(cursorCLIPaneFixture(t, "shell-approval"), "$  echo agentman-research in .", "", 1)
	if q := cursorCLIQuestionFromPane(pane); q != nil {
		t.Fatalf("approval without its command was accepted: %+v", q)
	}
}

// Prompt placeholders Cursor uses while it waits on something the phone has
// no layout for, and while the prompt is in shell mode.
func TestCursorCLIPlaceholdersThatBlockSending(t *testing.T) {
	bar := func(placeholder string) string {
		return "  Working on it\n" +
			" ▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄\n" +
			"  → " + placeholder + "\n" +
			" ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀\n" +
			"  Auto\n"
	}
	for _, placeholder := range []string{
		"Approve mode switch (y/n)",
		"Answer questions (Enter to select/next, Esc to skip)",
		"Waiting for decision (y/n/p)...",
		"Edit the image prompt, then press Enter to generate",
	} {
		screen := parseCursorCLIScreen(bar(placeholder))
		if !screen.needsUser || screen.blocked == "" || screen.question != nil {
			t.Errorf("%q: needsUser=%v blocked=%q question=%v", placeholder, screen.needsUser, screen.blocked, screen.question)
		}
	}
	shell := parseCursorCLIScreen(bar("Run a command — e.g., ls"))
	if shell.blocked == "" || shell.needsUser {
		t.Errorf("shell mode: %+v; a phone message would run as a shell command", shell)
	}
	idle := parseCursorCLIScreen(bar("Add a follow-up"))
	if idle.blocked != "" || idle.needsUser {
		t.Errorf("idle prompt blocked: %+v", idle)
	}
}

// fakeCursorPane plays a pane: each typed key may move it to the next screen.
type fakeCursorPane struct {
	mu      sync.Mutex
	screens []string
	typed   []string
}

func (f *fakeCursorPane) capture(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.screens[0], nil
}

func (f *fakeCursorPane) key(_ context.Context, _ string, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typed = append(f.typed, "key:"+key)
	if len(f.screens) > 1 {
		f.screens = f.screens[1:]
	}
	return nil
}

func (f *fakeCursorPane) text(_ context.Context, _ string, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typed = append(f.typed, "text:"+text)
	return nil
}

func cursorCLIAnswerFixture(t *testing.T, screens ...string) (*CursorCLISource, *fakeCursorPane, string) {
	t.Helper()
	s, err := NewCursorCLISource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pane := &fakeCursorPane{screens: screens}
	s.capturePane, s.sendKey, s.sendText = pane.capture, pane.key, pane.text
	id := cursorCLIPaneIDPrefix + "agentman-cursor-test"
	s.sessions[id] = cursorCLISession{
		pane: "agentman-cursor-test",
		meta: protocol.Session{Question: cursorCLIQuestionFromPane(screens[0])},
	}
	return s, pane, id
}

func TestCursorCLIAnswersADeleteWithItsKey(t *testing.T) {
	s, pane, id := cursorCLIAnswerFixture(t, cursorCLIPaneFixture(t, "delete-approval"))
	q := s.sessions[id].meta.Question
	if err := s.Answer(context.Background(), id, protocol.QuestionAnswer{QuestionID: q.ID, OptionKey: "n"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(pane.typed, "|") != "key:n" {
		t.Fatalf("typed %v, want only n (Keep)", pane.typed)
	}
	if err := s.Answer(context.Background(), id, protocol.QuestionAnswer{QuestionID: q.ID, OptionKey: "x"}); err == nil {
		t.Fatal("an option the menu does not offer was typed")
	}
	if err := s.Answer(context.Background(), id, protocol.QuestionAnswer{QuestionID: q.ID, Text: "keep it"}); err == nil {
		t.Fatal("text was accepted by a menu with no answer box")
	}
}

// "Skip & tell the agent what to do instead" with text from the phone: n
// opens Cursor's box, and only once it is on screen is the text typed.
func TestCursorCLISkipsAShellCommandWithAnInstruction(t *testing.T) {
	s, pane, id := cursorCLIAnswerFixture(t,
		cursorCLIPaneFixture(t, "shell-approval"), cursorCLIPaneFixture(t, "skip-box"))
	q := s.sessions[id].meta.Question
	if err := s.Answer(context.Background(), id, protocol.QuestionAnswer{QuestionID: q.ID, Text: "use printf instead"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(pane.typed, "|"); got != "key:n|text:use printf instead" {
		t.Fatalf("typed %q", got)
	}
}

// If Cursor never opens the box, the text must not be typed into the menu,
// where every letter is a shortcut.
func TestCursorCLIDoesNotTypeAnInstructionIntoTheMenu(t *testing.T) {
	approval := cursorCLIPaneFixture(t, "shell-approval")
	s, pane, id := cursorCLIAnswerFixture(t, approval, approval)
	q := s.sessions[id].meta.Question
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := s.Answer(ctx, id, protocol.QuestionAnswer{QuestionID: q.ID, Text: "yes do it"})
	if err == nil {
		t.Fatal("answer succeeded although the box never opened")
	}
	for _, typed := range pane.typed {
		if strings.HasPrefix(typed, "text:") {
			t.Fatalf("text typed into the menu: %v", pane.typed)
		}
	}
}

func TestCursorCLIAnswersAnOpenBoxDirectly(t *testing.T) {
	s, pane, id := cursorCLIAnswerFixture(t, cursorCLIPaneFixture(t, "plan-revise-box"))
	q := s.sessions[id].meta.Question
	if err := s.Answer(context.Background(), id, protocol.QuestionAnswer{QuestionID: q.ID, Text: "say ciao instead"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(pane.typed, "|"); got != "text:say ciao instead" {
		t.Fatalf("typed %q", got)
	}
	if err := s.Answer(context.Background(), id, protocol.QuestionAnswer{QuestionID: q.ID, Text: "bad\x1b[2J"}); err == nil {
		t.Fatal("an answer with a terminal escape was typed")
	}
}

// Every Cursor menu reads single letters, so a message typed into one is a
// run of choices: "build it" would build the plan.
func TestCursorCLIRefusesToTypeIntoMenusAndPickers(t *testing.T) {
	for _, name := range []string{
		"delete-approval", "plan-approval-focus-2", "shell-approval", "skip-box",
		"plan-revise-box", "resume-picker", "trust",
	} {
		s, pane, id := cursorCLIAnswerFixture(t, cursorCLIPaneFixture(t, name))
		if _, err := s.Inject(context.Background(), id, "build it"); err == nil {
			t.Errorf("%s: send was typed into the pane", name)
		}
		if len(pane.typed) != 0 {
			t.Errorf("%s: typed %v", name, pane.typed)
		}
	}
	s, pane, id := cursorCLIAnswerFixture(t, cursorCLIPaneFixture(t, "todos-done"))
	if mode, err := s.Inject(context.Background(), id, "- next step"); err != nil || mode != protocol.InjectTmux {
		t.Fatalf("send at the prompt: %v, %v", mode, err)
	}
	if strings.Join(pane.typed, "|") != "text:- next step" {
		t.Fatalf("typed %v", pane.typed)
	}
}

// Ctrl-C only means "stop" while a turn runs: at a plan menu it rejects the
// plan, and at an idle prompt a second one quits the CLI.
func TestCursorCLIInterruptOnlyStopsARunningTurn(t *testing.T) {
	for _, name := range []string{"todos-done", "plan-approval-focus-2"} {
		s, _, id := cursorCLIAnswerFixture(t, cursorCLIPaneFixture(t, name))
		err := s.Interrupt(context.Background(), id)
		if err == nil || errors.Is(err, context.Canceled) {
			t.Errorf("%s: interrupt was sent: %v", name, err)
		}
	}
}

// Discovery reports a pane waiting on a decision the phone cannot answer as
// waiting, without a question, rather than as idle.
func TestCursorCLIPaneStateReportsUnreadableDecisionsAsWaiting(t *testing.T) {
	s, _, _ := cursorCLIAnswerFixture(t, " ▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄\n  → Approve mode switch (y/n)\n ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀\n")
	state, q := s.cursorCLIPaneState(context.Background(), "agentman-cursor-test")
	if state != protocol.StateWaitingInput || q != nil {
		t.Fatalf("state = %s, question = %v", state, q)
	}
	s, _, _ = cursorCLIAnswerFixture(t, cursorCLIPaneFixture(t, "reconnecting"))
	if state, _ := s.cursorCLIPaneState(context.Background(), "agentman-cursor-test"); state != protocol.StateBusy {
		t.Fatalf("reconnecting turn reads %s, want busy", state)
	}
}
