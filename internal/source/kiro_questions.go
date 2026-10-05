package source

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	if current == nil || !sameQuestion(shown, protocolQuestion(current)) {
		return fmt.Errorf("source: that question is no longer on screen; refresh the session")
	}
	if back {
		return tmux.Escape(ctx, tmuxName)
	}
	// Send clears whatever was typed at the Mac first, so the reason sent is
	// exactly the one written on the phone.
	return tmux.Send(ctx, tmuxName, reason)
}
