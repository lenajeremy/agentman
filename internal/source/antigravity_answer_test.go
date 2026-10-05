package source

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// agyFixture reads a pane captured from agy 1.2.17 (100 columns, paths
// shortened).
func agyFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/antigravity-pane-" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// agyKeys records what an answer pressed, and plays back the panes agy would
// show in between.
type agyKeys struct {
	events []string
	panes  []string
	// escapes are what Escape shows, one per press: in agy's subagent panel
	// the first leaves the choice and the second closes the panel.
	escapes []string
}

// capture returns the next pane in line, staying on the last one.
func (k *agyKeys) capture(context.Context, string) (string, error) {
	pane := k.panes[0]
	if len(k.panes) > 1 {
		k.panes = k.panes[1:]
	}
	return pane, nil
}

func (k *agyKeys) keys(withSend bool) antigravityKeys {
	keys := antigravityKeys{
		typeText: func(_ context.Context, _, text string) error {
			k.events = append(k.events, "type "+text)
			return nil
		},
		press: func(_ context.Context, _, key string) error {
			k.events = append(k.events, "press "+key)
			return nil
		},
		writeIn: func(_ context.Context, _, key, text string) error {
			k.events = append(k.events, "write-in "+key+" "+text)
			return nil
		},
		confirm: func(ctx context.Context, name string, distance int, ready func(string) bool) error {
			pane, _ := k.capture(ctx, name)
			if !ready(pane) {
				k.events = append(k.events, "refused")
				return tmuxRefused
			}
			k.events = append(k.events, "move "+strings.Repeat("↓", max(distance, 0))+strings.Repeat("↑", max(-distance, 0))+" enter")
			return nil
		},
	}
	if withSend {
		keys.send = func(_ context.Context, _ string, names ...string) error {
			k.events = append(k.events, "send "+strings.Join(names, " "))
			if len(names) == 1 && names[0] == "Escape" && len(k.escapes) > 0 {
				k.panes, k.escapes = []string{k.escapes[0]}, k.escapes[1:]
			}
			return nil
		}
	}
	return keys
}

var tmuxRefused = errors.New("tmux: that choice is no longer on screen")

// agyAsking discovers a pane-backed session showing pane, so it carries the
// question the phone would have been shown.
func agyAsking(t *testing.T, pane string, withSend bool) (*AntigravitySource, *agyKeys, string) {
	t.Helper()
	home := t.TempDir()
	writeAntigravityConversation(t, home, agConversation, agLinePrompt, agLineCall)
	tmuxPane := tmux.Session{Name: "agentman-antigravity-1-a", PanePID: 900, Cwd: "/work/ws1"}
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/work/ws1", conversations: []string{agConversation}},
	}, tmuxPane)
	keys := &agyKeys{panes: []string{pane}}
	s.capturePane = keys.capture
	s.keys = keys.keys(withSend)
	id := "antigravity:tmux-agentman-antigravity-1-a"
	if discoverAntigravity(t, s)[id].Question == nil {
		t.Fatal("the fixture shows no question")
	}
	return s, keys, id
}

func agyAnswer(t *testing.T, s *AntigravitySource, id string, answer protocol.QuestionAnswer) error {
	t.Helper()
	answer.QuestionID = s.sessions[id].meta.Question.ID
	return s.Answer(context.Background(), id, answer)
}

// A permission prompt is answered by moving to the row and pressing Enter
// once the screen shows that row focused.
func TestAntigravityAnswersAPermissionPrompt(t *testing.T) {
	pane := agyFixture(t, "command")
	s, keys, id := agyAsking(t, pane, false)
	focused := strings.Replace(strings.Replace(pane, "> 1. Yes, run command", "  1. Yes, run command", 1),
		"  4. No, cancel", "> 4. No, cancel", 1)
	keys.panes = []string{pane, focused}
	if err := agyAnswer(t, s, id, protocol.QuestionAnswer{OptionKey: "4"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(keys.events, ";") != "move ↓↓↓ enter" {
		t.Errorf("keys = %q", keys.events)
	}
}

// "Write-in..." opens a text box on its digit; the answer is typed there.
func TestAntigravityAnswersAQuestionInWords(t *testing.T) {
	s, keys, id := agyAsking(t, agyFixture(t, "question"), false)
	if err := agyAnswer(t, s, id, protocol.QuestionAnswer{Text: "  purple "}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(keys.events, ";") != "write-in 4 purple" {
		t.Errorf("keys = %q", keys.events)
	}
	// Enter submits agy's box: a second line would go as an answer of its own.
	if err := agyAnswer(t, s, id, protocol.QuestionAnswer{Text: "purple\nand green"}); err == nil {
		t.Error("a two-line answer was typed")
	}
}

// Text for a permission prompt has nowhere to go.
func TestAntigravityRefusesWordsForAPermissionPrompt(t *testing.T) {
	s, keys, id := agyAsking(t, agyFixture(t, "command"), false)
	if err := agyAnswer(t, s, id, protocol.QuestionAnswer{Text: "yes please"}); err == nil || len(keys.events) > 0 {
		t.Errorf("err %v keys %q", err, keys.events)
	}
}

// Someone is typing into the question at the Mac. A digit now would be typed
// into their answer.
func TestAntigravityLeavesAQuestionBeingTypedAlone(t *testing.T) {
	question := agyFixture(t, "question")
	s, keys, id := agyAsking(t, question, false)
	keys.panes = []string{agyFixture(t, "question-writein-typing")}
	err := agyAnswer(t, s, id, protocol.QuestionAnswer{OptionKey: "1"})
	if err == nil || len(keys.events) > 0 {
		t.Errorf("err %v keys %q", err, keys.events)
	}
}

// A multi-select row toggles on its digit. Only the rows that differ are
// pressed, and the form moves on only once the screen shows exactly the
// chosen rows ticked.
func TestAntigravityAnswersAMultiSelectQuestion(t *testing.T) {
	s, keys, id := agyAsking(t, agyFixture(t, "question-multi"), true)
	keys.panes = []string{agyFixture(t, "question-multi"), agyFixture(t, "question-multi-checked")}
	if err := agyAnswer(t, s, id, protocol.QuestionAnswer{Options: []string{"1", "3"}}); err != nil {
		t.Fatal(err)
	}
	// Question 1 of 2: → moves on; Enter would submit the form early.
	if got := strings.Join(keys.events, ";"); got != "press 1;press 3;send Right" {
		t.Errorf("keys = %q", got)
	}

	// The screen disagreeing is not answered over.
	s, keys, id = agyAsking(t, agyFixture(t, "question-multi"), true)
	keys.panes = []string{agyFixture(t, "question-multi"), agyFixture(t, "question-multi")}
	if err := agyAnswer(t, s, id, protocol.QuestionAnswer{Options: []string{"1", "3"}}); err == nil ||
		strings.Contains(strings.Join(keys.events, ";"), "send") {
		t.Errorf("err %v keys %q", err, keys.events)
	}

	// Without a way to press →, nothing is pressed at all.
	s, keys, id = agyAsking(t, agyFixture(t, "question-multi"), false)
	if err := agyAnswer(t, s, id, protocol.QuestionAnswer{Options: []string{"1"}}); err == nil || len(keys.events) > 0 {
		t.Errorf("err %v keys %q", err, keys.events)
	}
}

// The last question of a form submits on Enter ("enter Submit All").
func TestAntigravitySubmitsTheLastMultiSelectQuestion(t *testing.T) {
	last := func(pane string) string {
		pane = strings.Replace(pane, "Question 1/2:", "Question 2/2:", 1)
		return strings.Replace(pane, "→ Next · space Toggle · enter Submit ·", "← Back · space Toggle · enter Submit All ·", 1)
	}
	s, keys, id := agyAsking(t, last(agyFixture(t, "question-multi")), false)
	keys.panes = []string{last(agyFixture(t, "question-multi")), last(agyFixture(t, "question-multi-checked"))}
	if err := agyAnswer(t, s, id, protocol.QuestionAnswer{Options: []string{"1", "3"}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(keys.events, ";"); got != "press 1;press 3;move  enter" {
		t.Errorf("keys = %q", got)
	}
}

// A subagent's request is answered in its panel, which is closed afterwards
// — with Escape, and only while it is visibly open. Panes captured from agy
// 1.2.17: the request box, the panel alt+j opened, and the panel after the
// answer, which stays open until Escape.
func TestAntigravityAnswersASubagentInItsPanel(t *testing.T) {
	for _, tc := range []struct {
		box, panel, key string
	}{
		{"subagent-approval", "subagent-panel", "2"},
		{"subagent-approval-command", "subagent-panel-command", "1"},
	} {
		s, keys, id := agyAsking(t, agyFixture(t, tc.box), true)
		keys.panes = []string{agyFixture(t, tc.box), agyFixture(t, tc.panel), agyFixture(t, "subagent-panel-answered")}
		keys.escapes = []string{agyFixture(t, "idle-after-reply")}
		if err := agyAnswer(t, s, id, protocol.QuestionAnswer{OptionKey: tc.key}); err != nil {
			t.Fatalf("%s: %v", tc.box, err)
		}
		if got := strings.Join(keys.events, ";"); got != "send M-j;press "+tc.key+";send Escape" {
			t.Errorf("%s: keys = %q", tc.box, got)
		}
	}

	// The panel closing by itself leaves nothing for Escape to do — and on
	// the prompt, Escape would cancel the parent's turn.
	s, keys, id := agyAsking(t, agyFixture(t, "subagent-approval"), true)
	keys.panes = []string{
		agyFixture(t, "subagent-approval"),
		agyFixture(t, "subagent-panel"),
		agyFixture(t, "idle-after-reply"),
	}
	if err := agyAnswer(t, s, id, protocol.QuestionAnswer{OptionKey: "1"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(keys.events, ";"); got != "send M-j;press 1" {
		t.Errorf("keys = %q", got)
	}
}

// Nothing is chosen unless the panel shows the same subagent, tool and
// argument as the request the phone was shown, offering exactly agy's two
// choices. On a mismatch the panel is closed — Escape there never answers,
// the first leaving the choice and the second closing it — and the request is
// left waiting.
func TestAntigravityChoosesNothingInAPanelShowingSomethingElse(t *testing.T) {
	command := agyFixture(t, "subagent-panel-command")
	for name, panel := range map[string]string{
		// A different subagent and request altogether.
		"another request": agyFixture(t, "subagent-panel"),
		"another argument": strings.Replace(command, "      echo subagent-check\n",
			"      rm -rf /work/ws1\n", 1),
		"other choices": strings.Replace(command, "2. No, deny", "2. No, deny and stop the subagent", 1),
		"another agent": strings.Replace(command, "main › self", "main › research", 1),
	} {
		s, keys, id := agyAsking(t, agyFixture(t, "subagent-approval-command"), true)
		keys.panes = []string{agyFixture(t, "subagent-approval-command"), panel}
		keys.escapes = []string{agyFixture(t, "subagent-panel-viewing"), agyFixture(t, "subagent-approval-after-close")}
		err := agyAnswer(t, s, id, protocol.QuestionAnswer{OptionKey: "1"})
		if err == nil || !strings.Contains(err.Error(), "nothing was chosen") {
			t.Errorf("%s: err %v", name, err)
		}
		if got := strings.Join(keys.events, ";"); got != "send M-j;send Escape;send Escape" {
			t.Errorf("%s: keys = %q", name, got)
		}
	}
}

// A digit that did not take leaves the request waiting: the panel is closed
// and the phone told, rather than a second key pressed.
func TestAntigravityReportsASubagentAnswerThatDidNotTake(t *testing.T) {
	s, keys, id := agyAsking(t, agyFixture(t, "subagent-approval-command"), true)
	keys.panes = []string{agyFixture(t, "subagent-approval-command"), agyFixture(t, "subagent-panel-command")}
	keys.escapes = []string{agyFixture(t, "subagent-panel-viewing"), agyFixture(t, "subagent-approval-after-close")}
	err := agyAnswer(t, s, id, protocol.QuestionAnswer{OptionKey: "2"})
	if err == nil || !strings.Contains(err.Error(), "not answered") {
		t.Errorf("err %v", err)
	}
	if got := strings.Join(keys.events, ";"); got != "send M-j;press 2;send Escape;send Escape" {
		t.Errorf("keys = %q", got)
	}
}

// A panel that will not close is said to be open, so the user knows to close
// it at the Mac — and Escape is never pressed past it.
func TestAntigravitySaysWhenTheSubagentPanelStaysOpen(t *testing.T) {
	s, keys, id := agyAsking(t, agyFixture(t, "subagent-approval-command"), true)
	keys.panes = []string{agyFixture(t, "subagent-approval-command"), agyFixture(t, "subagent-panel")}
	err := agyAnswer(t, s, id, protocol.QuestionAnswer{OptionKey: "1"})
	if err == nil || !strings.Contains(err.Error(), "still open on the Mac") {
		t.Errorf("err %v", err)
	}
	if got := strings.Join(keys.events, ";"); got != "send M-j;send Escape;send Escape" {
		t.Errorf("keys = %q", got)
	}
}

func TestAntigravityRequestArgument(t *testing.T) {
	for detail, want := range map[string]string{
		"Read(~/.zsh_history)":      "/.zsh_history",
		"Bash(echo subagent-check)": "echo subagent-check",
		"Edit(~/.gemini/antigravity-cli/brain/ef...ed0f3cca68b/implementation_plan.md)": "ed0f3cca68b/implementation_plan.md",
		"Bash":          "",
		"Read(notes.md": "",
	} {
		tool, _, _ := strings.Cut(detail, "(")
		if got := antigravityRequestArgument(detail, tool); got != want {
			t.Errorf("%q: %q, want %q", detail, got, want)
		}
	}
}

func TestAntigravityRefusesAStaleAnswer(t *testing.T) {
	s, keys, id := agyAsking(t, agyFixture(t, "command"), false)
	err := s.Answer(context.Background(), id, protocol.QuestionAnswer{QuestionID: "terminal-old", OptionKey: "1"})
	if err == nil || len(keys.events) > 0 {
		t.Errorf("err %v keys %q", err, keys.events)
	}
	// Same id, but the screen moved on to another prompt.
	keys.panes = []string{agyFixture(t, "question")}
	if err := agyAnswer(t, s, id, protocol.QuestionAnswer{OptionKey: "1"}); err == nil || len(keys.events) > 0 {
		t.Errorf("err %v keys %q", err, keys.events)
	}
}

// A message typed while a panel or picker is open lands in it — a search in
// /model, approvals in the review panel — so it is refused instead.
func TestAntigravityRefusesToTypeIntoAPanel(t *testing.T) {
	home := t.TempDir()
	writeAntigravityConversation(t, home, agConversation, agLinePrompt, agLineReply)
	pane := tmux.Session{Name: "agentman-antigravity-1-a", PanePID: 900, Cwd: "/work/ws1"}
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/work/ws1", conversations: []string{agConversation}},
	}, pane)
	keys := &agyKeys{panes: []string{agyFixture(t, "idle-after-reply")}}
	s.capturePane = keys.capture
	s.keys = keys.keys(false)
	id := "antigravity:tmux-agentman-antigravity-1-a"
	discoverAntigravity(t, s)

	keys.panes = []string{agyFixture(t, "model-picker")}
	if _, err := s.Inject(context.Background(), id, "hello"); err == nil || len(keys.events) > 0 {
		t.Errorf("typed into the model picker: err %v keys %q", err, keys.events)
	}
	keys.panes = []string{agyFixture(t, "subagent-approval")}
	if _, err := s.Inject(context.Background(), id, "hello"); err == nil || len(keys.events) > 0 {
		t.Errorf("typed over a pending request: err %v keys %q", err, keys.events)
	}
	keys.panes = []string{agyFixture(t, "idle-after-reply")}
	if mode, err := s.Inject(context.Background(), id, "hello"); err != nil || mode != protocol.InjectTmux ||
		strings.Join(keys.events, ";") != "type hello" {
		t.Errorf("mode %s err %v keys %q", mode, err, keys.events)
	}
}

// tmux.SendKeys refuses a key outside its list without pressing anything; the
// answer then fails before the pane is touched.
func TestAntigravitySubagentAnswerStopsWhenItsKeyIsRefused(t *testing.T) {
	s, keys, id := agyAsking(t, agyFixture(t, "subagent-approval"), true)
	s.keys.send = func(context.Context, string, ...string) error {
		return errors.New(`tmux: "M-j" is not a key Agentman presses`)
	}
	err := agyAnswer(t, s, id, protocol.QuestionAnswer{OptionKey: "1"})
	if err == nil || !strings.Contains(err.Error(), "on the Mac") || len(keys.events) > 0 {
		t.Errorf("err %v keys %q", err, keys.events)
	}
}

// "tab Amend" approves with a message for the agent. The approval is offered
// with a note; with no note it is the plain approval, and nothing else takes
// one.
func TestAntigravityApprovesWithANote(t *testing.T) {
	pane := agyFixture(t, "command")
	s, keys, id := agyAsking(t, pane, true)
	offered := s.sessions[id].meta.Question
	if !offered.Options[0].WithText || offered.Options[1].WithText || offered.Options[3].WithText {
		t.Fatalf("options = %+v", offered.Options)
	}
	amending := strings.Replace(strings.Replace(pane,
		"> 1. Yes, run command\n", "> 1. Yes, and tell Antigravity CLI what to do next\n    > █\n", 1),
		"  ↑/↓ Navigate · tab Amend · ctrl+g edit/expand command", "  enter Submit", 1)
	keys.panes = []string{pane, amending}
	if err := agyAnswer(t, s, id, protocol.QuestionAnswer{OptionKey: "1", Text: " then show me the output "}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(keys.events, ";"); got != "send Tab;press then show me the output;send Enter" {
		t.Errorf("keys = %q", got)
	}

	// Tab did not open the box: nothing is typed into whatever is there.
	s, keys, id = agyAsking(t, pane, true)
	keys.panes = []string{pane, pane}
	if err := agyAnswer(t, s, id, protocol.QuestionAnswer{OptionKey: "1", Text: "note"}); err == nil ||
		strings.Join(keys.events, ";") != "send Tab" {
		t.Errorf("err %v keys %q", err, keys.events)
	}

	// A note on a choice that takes none is refused before any key.
	s, keys, id = agyAsking(t, pane, true)
	if err := agyAnswer(t, s, id, protocol.QuestionAnswer{OptionKey: "4", Text: "because"}); err == nil || len(keys.events) > 0 {
		t.Errorf("err %v keys %q", err, keys.events)
	}
	// A prompt without "tab Amend" offers no note at all.
	s, _, id = agyAsking(t, agyFixture(t, "file-access"), true)
	if s.sessions[id].meta.Question.Options[0].WithText {
		t.Error("a note was offered where agy takes none")
	}
}
