package source

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// antigravityKeys is how text and answers reach an agy pane. Injectable so the
// rules for answering can be tested without a tmux server.
type antigravityKeys struct {
	// typeText sends a message to the prompt (tmux.Send).
	typeText func(ctx context.Context, name, text string) error
	// press types one key — an option's digit — and nothing else
	// (tmux.Answer). In agy a digit chooses a single-choice row outright and
	// toggles a multi-select one.
	press func(ctx context.Context, name, key string) error
	// writeIn presses a row's digit, types into the text box it opens and
	// submits it (tmux.AnswerCustom).
	writeIn func(ctx context.Context, name, key, text string) error
	// confirm moves the cursor by distance rows, checks the pane shows what
	// the caller expects, and only then presses Enter (tmux.AnswerArrowMenu).
	confirm func(ctx context.Context, name string, distance int, ready func(pane string) bool) error
	// send presses named keys — Right to move to a form's next question,
	// alt+j to open the subagent panel, Escape to close it. Nil until the
	// daemon provides a way to send named keys safely; the answers that need
	// it are refused until then rather than approximated.
	send func(ctx context.Context, name string, keys ...string) error
}

func defaultAntigravityKeys() antigravityKeys {
	return antigravityKeys{
		typeText: tmux.Send,
		press:    tmux.Answer,
		writeIn:  tmux.AnswerCustom,
		confirm:  tmux.AnswerArrowMenu,
	}
}

// antigravityKeyPause lets agy redraw between two keys. It reads keys in order,
// but a check made against the screen has to see the first one's effect.
const antigravityKeyPause = 60 * time.Millisecond

// Answer implements Answerer.
//
// Every answer starts by reading the pane again and comparing what it shows
// with the question the phone was given; any difference means the screen
// moved on, and the answer is refused rather than applied to something the
// user never saw.
func (s *AntigravitySource) Answer(ctx context.Context, sessionID string, answer protocol.QuestionAnswer) error {
	session, err := s.session(sessionID)
	if err != nil {
		return err
	}
	if session.tmuxName == "" {
		return errors.New("source: only sessions started through Agentman can be answered")
	}
	shown := session.meta.Question
	if shown == nil || answer.QuestionID == "" || answer.QuestionID != shown.ID {
		return errors.New("source: that question is no longer current; refresh the session")
	}
	current, form, err := s.currentForm(ctx, session.tmuxName, shown)
	if err != nil {
		return err
	}
	if form.Typing {
		return errors.New("source: a reply is being typed into this question on the Mac; finish it there")
	}
	if answer.OptionKey == "" && len(answer.Options) == 1 && !current.Multiple {
		answer.OptionKey, answer.Options = answer.Options[0], nil
	}

	name := session.tmuxName
	switch {
	case form.Kind == "subagent":
		return s.answerAntigravitySubagent(ctx, name, current, answer)
	case strings.TrimSpace(answer.Text) != "":
		return s.answerAntigravityWriteIn(ctx, name, current, form, answer)
	case current.Multiple:
		return s.answerAntigravityChecks(ctx, name, shown, current, form, answer)
	}
	if len(answer.Options) > 0 || answer.OptionKey == "" {
		return errors.New("source: choose one of the listed options")
	}
	target := optionIndex(current.Options, answer.OptionKey)
	if target < 0 {
		return errors.New("source: that option is no longer on screen; refresh the session")
	}
	label := current.Options[target].Label
	return s.keys.confirm(ctx, name, target-current.FocusIndex, func(pane string) bool {
		now, _ := question.DetectAntigravityForm(pane)
		return now != nil && now.FocusIndex == target &&
			target < len(now.Options) && now.Options[target].Label == label
	})
}

// currentForm reads the pane and checks it still shows the question the phone
// was answering.
func (s *AntigravitySource) currentForm(
	ctx context.Context, name string, shown *protocol.Question,
) (*question.Question, question.AntigravityForm, error) {
	pane, err := s.capturePane(ctx, name)
	if err != nil {
		return nil, question.AntigravityForm{}, fmt.Errorf("source: could not inspect the question: %w", err)
	}
	current, form := question.DetectAntigravityForm(pane)
	if current == nil || !sameQuestion(shown, protocolQuestion(current)) {
		return nil, form, errors.New("source: that question is no longer on screen; refresh the session")
	}
	return current, form, nil
}

func optionIndex(options []question.Option, key string) int {
	for i, option := range options {
		if option.Key == key {
			return i
		}
	}
	return -1
}

// answerAntigravityWriteIn answers an ask_question form in the user's own
// words. Its "Write-in..." row opens a text box on its digit, which takes the
// text and submits on Enter — the sequence captured from agy 1.2.17.
func (s *AntigravitySource) answerAntigravityWriteIn(
	ctx context.Context, name string, current *question.Question,
	form question.AntigravityForm, answer protocol.QuestionAnswer,
) error {
	if form.Kind != "question" || form.WriteInKey == "" || current.Multiple {
		return errors.New("source: this question takes one of its options, not text")
	}
	if answer.OptionKey != "" || len(answer.Options) > 0 {
		return errors.New("source: answer with an option or with text, not both")
	}
	text := strings.TrimSpace(answer.Text)
	if strings.ContainsAny(text, "\r\n") {
		// Enter submits agy's box, so a second line would be cut off and sent
		// as an answer of its own.
		return errors.New("source: keep a written answer to one line")
	}
	return s.keys.writeIn(ctx, name, form.WriteInKey, text)
}

// answerAntigravityChecks answers a multi-select question: each digit toggles
// its row, so only the rows whose state differs are pressed, the result is
// read back off the screen, and only a form showing exactly the chosen rows is
// submitted.
//
// The last question of a form submits with Enter ("enter Submit All"). An
// earlier one moves on with →; Enter there submits the whole form early.
func (s *AntigravitySource) answerAntigravityChecks(
	ctx context.Context, name string, shown *protocol.Question, current *question.Question,
	form question.AntigravityForm, answer protocol.QuestionAnswer,
) error {
	want := map[string]bool{}
	for _, key := range append(append([]string{}, answer.Options...), answer.OptionKey) {
		if key == "" {
			continue
		}
		if optionIndex(current.Options, key) < 0 {
			return errors.New("source: that option is no longer on screen; refresh the session")
		}
		want[key] = true
	}
	if len(want) == 0 {
		return errors.New("source: choose at least one option")
	}
	last := form.Count == 0 || form.Index >= form.Count
	if !last && s.keys.send == nil {
		return errors.New("source: this form has more questions; answer it on the Mac")
	}
	for _, option := range current.Options {
		if want[option.Key] == option.Checked {
			continue
		}
		if err := s.keys.press(ctx, name, option.Key); err != nil {
			return err
		}
		time.Sleep(antigravityKeyPause)
	}
	matches := func(pane string) bool {
		now, nowForm := question.DetectAntigravityForm(pane)
		if now == nil || nowForm.Index != form.Index || !sameQuestion(shown, protocolQuestion(now)) {
			return false
		}
		for _, option := range now.Options {
			if want[option.Key] != option.Checked {
				return false
			}
		}
		return true
	}
	if last {
		return s.keys.confirm(ctx, name, 0, matches)
	}
	pane, err := s.capturePane(ctx, name)
	if err != nil {
		return err
	}
	if !matches(pane) {
		return errors.New("source: the choices did not take on the Mac; refresh the session")
	}
	return s.keys.send(ctx, name, "Right")
}

// answerAntigravitySubagent answers a subagent's permission request.
//
// The request is shown over the parent's prompt but answered in agy's subagent
// panel: alt+j opens it on the waiting request, a digit answers it, and the
// panel stays open afterwards until Escape closes it. Escape is pressed only
// while the panel is visibly open — on the prompt it would cancel the parent's
// turn.
func (s *AntigravitySource) answerAntigravitySubagent(
	ctx context.Context, name string, current *question.Question, answer protocol.QuestionAnswer,
) error {
	if answer.OptionKey != "1" && answer.OptionKey != "2" {
		return errors.New("source: choose one of the listed options")
	}
	if s.keys.send == nil {
		return errors.New("source: approve or deny a subagent's request on the Mac for now")
	}
	_, tool, _ := strings.Cut(current.Prompt, " needs approval for ")
	if err := s.keys.send(ctx, name, "M-j"); err != nil {
		return err
	}
	if err := s.waitForPanel(ctx, name, func(pending string, open bool) bool {
		return open && pending == tool
	}); err != nil {
		return errors.New("source: the subagent's request did not open on the Mac; answer it there")
	}
	if err := s.keys.press(ctx, name, answer.OptionKey); err != nil {
		return err
	}
	var open bool
	if err := s.waitForPanel(ctx, name, func(pending string, isOpen bool) bool {
		open = isOpen
		return pending == ""
	}); err != nil {
		return errors.New("source: the subagent's request was not answered; check the Mac")
	}
	if !open {
		return nil
	}
	return s.keys.send(ctx, name, "Escape")
}

// waitForPanel reads the pane until done says the subagent panel is in the
// state wanted, for about a second.
func (s *AntigravitySource) waitForPanel(
	ctx context.Context, name string, done func(pending string, open bool) bool,
) error {
	for attempt := 0; attempt < 15; attempt++ {
		pane, err := s.capturePane(ctx, name)
		if err != nil {
			return err
		}
		if done(question.AntigravitySubagentPanel(pane)) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(antigravityKeyPause):
		}
	}
	return errors.New("source: the panel did not reach the expected state")
}
