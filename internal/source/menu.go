package source

import (
	"context"
	"fmt"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// menuDetector reads the menu, if any, that one agent's terminal is blocked
// on. Kiro CLI and Antigravity CLI each draw menus the generic detector cannot
// read, so their adapters bring their own.
type menuDetector func(pane string) *question.Question

// paneMenu captures a pane and reads its pending menu.
func paneMenu(
	ctx context.Context,
	capture func(context.Context, string) (string, error),
	tmuxName string,
	detect menuDetector,
) (*question.Question, error) {
	if capture == nil {
		capture = tmux.Capture
	}
	pane, err := capture(ctx, tmuxName)
	if err != nil {
		return nil, err
	}
	return detect(pane), nil
}

// refuseSendIntoMenu is rejectSendIntoLiveQuestion for agents with their own
// menu shapes. Typing a message into an open menu does not queue it: the
// keystrokes land in the menu, and Enter can approve whatever row they left
// focused.
func refuseSendIntoMenu(
	ctx context.Context,
	capture func(context.Context, string) (string, error),
	tmuxName string,
	detect menuDetector,
) error {
	found, err := paneMenu(ctx, capture, tmuxName, detect)
	if err != nil {
		// Fail closed: without the pane there is no proof it is at a prompt.
		return fmt.Errorf("source: could not safely inspect the terminal before sending: %w", err)
	}
	if found != nil {
		return fmt.Errorf("source: answer the pending question before sending a message")
	}
	return nil
}

// answerMenu completes a single-choice menu by moving its cursor to the chosen
// row and pressing Enter.
//
// Two checks stand between a phone tap and the keystroke. The menu must still
// be the one the phone was shown — same question, same options — and after the
// cursor moves, a fresh capture must show it resting on the intended row.
// Either failing means the screen changed underneath the user, and the answer
// is refused rather than applied to something they never saw.
func answerMenu(
	ctx context.Context,
	capture func(context.Context, string) (string, error),
	tmuxName string,
	shown *protocol.Question,
	answer protocol.QuestionAnswer,
	detect menuDetector,
) error {
	if tmuxName == "" {
		return fmt.Errorf("source: only sessions started through Agentman can be answered")
	}
	if shown == nil || answer.QuestionID == "" || answer.QuestionID != shown.ID {
		return fmt.Errorf("source: that question is no longer current; refresh the session")
	}
	if len(answer.Options) > 0 || answer.Text != "" {
		return fmt.Errorf("source: choose one of the listed options")
	}
	current, err := paneMenu(ctx, capture, tmuxName, detect)
	if err != nil {
		return fmt.Errorf("source: could not inspect the question: %w", err)
	}
	if current == nil || !sameQuestion(shown, protocolQuestion(current)) {
		return fmt.Errorf("source: that question is no longer on screen; refresh the session")
	}
	target := -1
	for i, option := range current.Options {
		if option.Key == answer.OptionKey {
			target = i
			break
		}
	}
	if target < 0 {
		return fmt.Errorf("source: that option is no longer on screen; refresh the session")
	}
	label := current.Options[target].Label
	return tmux.AnswerArrowMenu(ctx, tmuxName, target-current.FocusIndex, func(pane string) bool {
		now := detect(pane)
		return now != nil && now.FocusIndex == target &&
			target < len(now.Options) && now.Options[target].Label == label
	})
}
