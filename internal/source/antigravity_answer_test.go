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
// — with Escape, and only while it is visibly open.
func TestAntigravityAnswersASubagentInItsPanel(t *testing.T) {
	s, keys, id := agyAsking(t, agyFixture(t, "subagent-approval"), true)
	keys.panes = []string{
		agyFixture(t, "subagent-approval"),
		agyFixture(t, "subagent-panel"),
		agyFixture(t, "subagent-panel-answered"),
	}
	if err := agyAnswer(t, s, id, protocol.QuestionAnswer{OptionKey: "2"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(keys.events, ";"); got != "send M-j;press 2;send Escape" {
		t.Errorf("keys = %q", got)
	}

	// The panel closing by itself leaves nothing for Escape to do — and on
	// the prompt, Escape would cancel the parent's turn.
	s, keys, id = agyAsking(t, agyFixture(t, "subagent-approval"), true)
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
