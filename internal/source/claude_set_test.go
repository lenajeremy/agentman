package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
)

// The AskUserQuestion call that opened the real form in testdata: three
// questions, Colour, Size and Shape.
const claudeSetTranscript = `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"AskUserQuestion","input":{"questions":[` +
	`{"question":"Which colour?","header":"Colour","multiSelect":false,"options":[{"label":"Red","description":"a warm colour"},{"label":"Blue","description":"a cool colour"}]},` +
	`{"question":"Which size?","header":"Size","multiSelect":false,"options":[{"label":"Small","description":"fits a pocket"},{"label":"Large","description":"fills a table"}]},` +
	`{"question":"Which shape?","header":"Shape","multiSelect":false,"options":[{"label":"Round","description":"no corners"},{"label":"Square","description":"four corners"}]}]}}]}}` + "\n"

// claudeOnFormStep is a session showing the real pane named, its transcript
// holding the form's call; moves records each move pressed.
func claudeOnFormStep(t *testing.T, pane string) (src *ClaudeSource, shown *protocol.Question, moves *[]int) {
	t.Helper()
	src, err := NewClaudeSource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(t.TempDir(), "s1.jsonl")
	if err := os.WriteFile(transcript, []byte(claudeSetTranscript), 0o600); err != nil {
		t.Fatal(err)
	}
	screen := permissionPane(t, pane)
	src.capturePane = func(context.Context, string) (string, error) { return screen, nil }
	detected := question.Detect(screen)
	if detected == nil {
		t.Fatalf("%s: no question", pane)
	}
	const id = "claude:s1"
	src.enrichClaudeQuestion(id, transcript, detected)
	shown = protocolQuestion(detected)
	src.sessions[id] = claudeSession{
		meta:       protocol.Session{ID: id, State: protocol.StateWaitingInput, Question: shown},
		transcript: transcript,
		tmuxName:   "agentman-claude-s1",
	}
	moves = &[]int{}
	src.moveQuestion = func(_ context.Context, name string, distance int) error {
		if name != "agentman-claude-s1" {
			t.Errorf("pressed in %q", name)
		}
		*moves = append(*moves, distance)
		return nil
	}
	return src, shown, moves
}

func TestTheQuestionOnScreenIsFoundInTheTranscript(t *testing.T) {
	_, shown, _ := claudeOnFormStep(t, "claude_set_second_after_answer_real_pane.txt")
	if shown.Step != 2 || len(shown.Steps) != 3 {
		t.Fatalf("step %d of %v", shown.Step, shown.Steps)
	}
	if !shown.Steps[0].Answered || shown.Steps[1].Answered || shown.Steps[1].Title != "Size" {
		t.Errorf("steps %v", shown.Steps)
	}
	// The steps are not part of the question's id: an answer to it still
	// matches once the form has moved on.
	bare := *shown
	bare.Steps, bare.Step = nil, 0
	if terminalQuestionID(&bare) != shown.ID {
		t.Error("the steps changed the question's id")
	}
	_, review, _ := claudeOnFormStep(t, "claude_set_review_partly_answered_real_pane.txt")
	if review.Step != 4 {
		t.Errorf("the review is step %d, want 4", review.Step)
	}
}

func TestMovingThroughAFormPressesTheArrowKeys(t *testing.T) {
	src, shown, moves := claudeOnFormStep(t, "claude_set_second_after_answer_real_pane.txt")
	for _, step := range []int{1, 3, 4, 2} {
		if err := src.MoveQuestion(context.Background(), "claude:s1", shown.ID, step); err != nil {
			t.Fatalf("to %d: %v", step, err)
		}
	}
	// From the second each time (the screen in the test doesn't move): back
	// one, on one, on two to the review, and none to stay.
	if want := []int{-1, 1, 2}; len(*moves) != 3 || (*moves)[0] != want[0] || (*moves)[1] != want[1] || (*moves)[2] != want[2] {
		t.Errorf("moves %v, want %v", *moves, want)
	}
}

func TestAMoveIsRefusedWhenItCouldLandSomewhereElse(t *testing.T) {
	src, shown, moves := claudeOnFormStep(t, "claude_set_second_after_answer_real_pane.txt")
	for name, try := range map[string]func() error{
		"a stale question": func() error { return src.MoveQuestion(context.Background(), "claude:s1", "terminal-old", 1) },
		"no such step":     func() error { return src.MoveQuestion(context.Background(), "claude:s1", shown.ID, 5) },
		"step zero":        func() error { return src.MoveQuestion(context.Background(), "claude:s1", shown.ID, 0) },
	} {
		if err := try(); err == nil {
			t.Errorf("%s was moved to", name)
		}
	}
	// The form on screen is no longer the one the app saw.
	src.capturePane = func(context.Context, string) (string, error) {
		return permissionPane(t, "claude_set_first_unanswered_real_pane.txt"), nil
	}
	if err := src.MoveQuestion(context.Background(), "claude:s1", shown.ID, 1); err == nil || !strings.Contains(err.Error(), "no longer") {
		t.Errorf("moved on a different screen: %v", err)
	}
	if len(*moves) != 0 {
		t.Errorf("keys pressed anyway: %v", *moves)
	}
}

// Without the transcript the question on screen can't be told, so there is
// no move: the keys could go the wrong way.
func TestNoMoveWithoutKnowingWhichQuestionIsOnScreen(t *testing.T) {
	src, shown, moves := claudeOnFormStep(t, "claude_set_second_after_answer_real_pane.txt")
	session := src.sessions["claude:s1"]
	session.transcript = filepath.Join(t.TempDir(), "missing.jsonl")
	src.sessions["claude:s1"] = session
	if err := src.MoveQuestion(context.Background(), "claude:s1", shown.ID, 1); err == nil {
		t.Error("moved without knowing the step")
	}
	if len(*moves) != 0 {
		t.Errorf("keys pressed: %v", *moves)
	}
}

func TestTheAgentsQuestionClosesWithEsc(t *testing.T) {
	for _, pane := range []string{"claude_set_second_after_answer_real_pane.txt", "claude_set_review_partly_answered_real_pane.txt"} {
		src, shown, _ := claudeOnFormStep(t, pane)
		if !shown.Closable {
			t.Errorf("%s: not closable", pane)
		}
		var pressed []string
		src.closeQuestion = func(_ context.Context, name string) error {
			pressed = append(pressed, name)
			return nil
		}
		if err := src.CloseQuestion(context.Background(), "claude:s1", shown.ID); err != nil {
			t.Fatalf("%s: %v", pane, err)
		}
		if len(pressed) != 1 || pressed[0] != "agentman-claude-s1" {
			t.Errorf("%s: Esc pressed in %v", pane, pressed)
		}
		if err := src.CloseQuestion(context.Background(), "claude:s1", "terminal-old"); err == nil {
			t.Errorf("%s: a stale question was closed", pane)
		}
	}
}

// Esc on a permission prompt is a no, not a close: it is answered instead.
func TestAPermissionPromptIsNotClosed(t *testing.T) {
	src, shown, _ := claudeOnFormStep(t, "claude_permission_real_pane.txt")
	if shown.Closable {
		t.Fatal("a permission prompt reads as closable")
	}
	pressed := 0
	src.closeQuestion = func(context.Context, string) error { pressed++; return nil }
	if err := src.CloseQuestion(context.Background(), "claude:s1", shown.ID); err == nil || pressed != 0 {
		t.Errorf("closed a permission prompt: %v, %d presses", err, pressed)
	}
}
