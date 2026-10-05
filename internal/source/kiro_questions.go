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

// detector reads the menu a Kiro pane is blocked on, naming the call under
// review from the transcript when the screen no longer shows it.
//
// The menu names the call on the "↓" row above it, and a long diff or a tall
// task list can push that row off an 80x24 pane. The call is on disk by then —
// Kiro records it before asking — and it is the newest one still running.
// Every path that reads a Kiro menu goes through here, discovery and answering
// alike, because the detail is part of the question's identity: an answer to
// a question shown with it must match the question read back without it.
func (s *KiroSource) detector(ctx context.Context, transcript, sessionID string) menuDetector {
	return func(pane string) *question.Question {
		found := question.DetectKiro(pane)
		if found == nil || found.Detail != "" || transcript == "" {
			return found
		}
		found.Detail = s.pendingCall(ctx, transcript, sessionID)
		return found
	}
}

// pendingCall describes the newest call in a transcript still waiting for its
// result, as Kiro's own approval screen does: "Shell ls -la".
func (s *KiroSource) pendingCall(ctx context.Context, transcript, sessionID string) string {
	messages, err := s.readTranscript(ctx, transcript, sessionID)
	if err != nil {
		return ""
	}
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role == protocol.RoleUser {
			return "" // a new turn: nothing before it is waiting
		}
		if message.Tool == nil || message.Tool.Status != protocol.ToolRunning {
			continue
		}
		return strings.TrimSpace(message.Tool.Name + " " + message.Tool.Summary)
	}
	return ""
}

// errKiroOverlay refuses a send while a picker or panel has the keyboard.
var errKiroOverlay = errors.New(
	"source: a Kiro menu is open on the Mac (/model, /agent, /rewind or a panel); close it there before sending")

// refuseSendIntoOverlay is refuseSendIntoMenu for everything Kiro draws over
// its prompt, not only approval menus.
//
// A message typed into the /model picker becomes its search, and the Enter
// that should submit it switches the model to whatever matched; in /rewind it
// forks the session. Neither is a question to answer from the phone, so the
// send is refused with a reason rather than typed somewhere it does harm.
func (s *KiroSource) refuseSendIntoOverlay(ctx context.Context, tmuxName string) error {
	capture := s.capturePane
	if capture == nil {
		capture = tmux.Capture
	}
	pane, err := capture(ctx, tmuxName)
	if err != nil {
		// Fail closed: without the pane there is no proof it is at a prompt.
		return fmt.Errorf("source: could not safely inspect the terminal before sending: %w", err)
	}
	if question.DetectKiro(pane) != nil {
		return fmt.Errorf("source: answer the pending question before sending a message")
	}
	if question.KiroOverlayOpen(pane) {
		return errKiroOverlay
	}
	return nil
}

// answerFeedback answers the editor Kiro opens for the reason a call is
// refused: typed text is the reason, sent with Enter, and the one listed
// choice is Escape, back to the approval menu.
//
// The same checks as answerMenu stand between the tap and the keystroke: the
// question must be the one the phone was shown, and still on screen.
func (s *KiroSource) answerFeedback(
	ctx context.Context,
	tmuxName string,
	shown *protocol.Question,
	answer protocol.QuestionAnswer,
	detect menuDetector,
) error {
	if tmuxName == "" {
		return fmt.Errorf("source: only sessions started through Agentman can be answered")
	}
	if answer.QuestionID == "" || answer.QuestionID != shown.ID {
		return fmt.Errorf("source: that question is no longer current; refresh the session")
	}
	reason := strings.TrimSpace(answer.Text)
	back := reason == "" && len(answer.Options) == 0 && answer.OptionKey == "1"
	if reason == "" && !back {
		return fmt.Errorf("source: write the reason, or go back to the choices")
	}
	current, err := paneMenu(ctx, s.capturePane, tmuxName, detect)
	if err != nil {
		return fmt.Errorf("source: could not inspect the question: %w", err)
	}
	if current == nil || !sameQuestion(shown, kiroQuestion(current)) {
		return fmt.Errorf("source: that question is no longer on screen; refresh the session")
	}
	if back {
		return s.sendKeys(ctx, tmuxName, "Escape")
	}
	// Send clears whatever was typed at the Mac first, so the reason sent is
	// exactly the one written on the phone.
	return s.sendText(ctx, tmuxName, reason)
}

// kiroQuestion is protocolQuestion for a Kiro menu, offering a reason with the
// refusal.
//
// Tab on "No" opens a box for the reason a call is refused, which Kiro hands
// the agent with the refusal. On the phone that is a note under "No"; every
// other choice stays one tap. Only the menu's first level has it: the trust
// options have no refusal.
func kiroQuestion(found *question.Question) *protocol.Question {
	q := protocolQuestion(found)
	if strings.HasSuffix(q.Prompt, " requires approval") {
		for i := range q.Options {
			q.Options[i].WithText = q.Options[i].Label == "No"
		}
	}
	return q
}

// kiroPaneSettle bounds how long an answer waits for Kiro to redraw after a
// key: focus moving to a row, or the reason box opening.
const kiroPaneSettle = 1500 * time.Millisecond

// waitForPane captures a pane until ready says it shows what was expected,
// for at most kiroPaneSettle.
func (s *KiroSource) waitForPane(ctx context.Context, tmuxName string, ready func(pane string) bool) bool {
	capture := s.capturePane
	if capture == nil {
		capture = tmux.Capture
	}
	deadline := time.Now().Add(kiroPaneSettle)
	for {
		if pane, err := capture(ctx, tmuxName); err == nil && ready(pane) {
			return true
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// answerWithReason refuses a call and says why: focus moves to "No", Tab
// opens Kiro's box for the reason, and the reason is typed into it and sent.
//
// Every step is checked on screen before the next key: the menu must be the
// one the phone was shown, the cursor must rest on "No" before Tab, and the
// box must be open before anything is typed — typed into the menu instead,
// the text would be keys that move focus and choose. If any check fails the
// answer stops there, with nothing chosen.
func (s *KiroSource) answerWithReason(
	ctx context.Context,
	tmuxName string,
	shown *protocol.Question,
	answer protocol.QuestionAnswer,
	detect menuDetector,
) error {
	if tmuxName == "" {
		return fmt.Errorf("source: only sessions started through Agentman can be answered")
	}
	if answer.QuestionID == "" || answer.QuestionID != shown.ID {
		return fmt.Errorf("source: that question is no longer current; refresh the session")
	}
	takesReason := false
	for _, option := range shown.Options {
		takesReason = takesReason || (option.Key == answer.OptionKey && option.WithText)
	}
	if !takesReason {
		return fmt.Errorf("source: only the refusal can carry a note")
	}
	current, err := paneMenu(ctx, s.capturePane, tmuxName, detect)
	if err != nil {
		return fmt.Errorf("source: could not inspect the question: %w", err)
	}
	if current == nil || !sameQuestion(shown, kiroQuestion(current)) {
		return fmt.Errorf("source: that question is no longer on screen; refresh the session")
	}
	target := -1
	for i, option := range current.Options {
		if option.Key == answer.OptionKey {
			target = i
		}
	}
	if target < 0 {
		return fmt.Errorf("source: that option is no longer on screen; refresh the session")
	}

	var moves []string
	for i := current.FocusIndex; i < target; i++ {
		moves = append(moves, "Down")
	}
	for i := current.FocusIndex; i > target; i-- {
		moves = append(moves, "Up")
	}
	if len(moves) > 0 {
		if err := s.sendKeys(ctx, tmuxName, moves...); err != nil {
			return err
		}
	}
	label := current.Options[target].Label
	if !s.waitForPane(ctx, tmuxName, func(pane string) bool {
		now := detect(pane)
		return now != nil && now.Prompt == current.Prompt && now.FocusIndex == target &&
			target < len(now.Options) && now.Options[target].Label == label
	}) {
		return fmt.Errorf("source: that choice is no longer on screen; refresh the session")
	}
	if err := s.sendKeys(ctx, tmuxName, "Tab"); err != nil {
		return err
	}
	editor := current.Prompt + " · " + question.KiroFeedbackLevel
	if !s.waitForPane(ctx, tmuxName, func(pane string) bool {
		now := question.DetectKiro(pane)
		return now != nil && now.Prompt == editor
	}) {
		return fmt.Errorf("source: Kiro did not open its box for a reason, so nothing was sent; answer on the Mac")
	}
	return s.sendText(ctx, tmuxName, strings.TrimSpace(answer.Text))
}
