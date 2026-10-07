package source

import (
	"context"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
)

// claudeWithDialogOpen is a session whose pane shows Claude Code's "Teach auto
// mode about your environment?" dialog, which it opens of its own accord after
// a turn. closed records each pane Esc was pressed in.
func claudeWithDialogOpen(t *testing.T) (src *ClaudeSource, questionID string, closed *[]string) {
	t.Helper()
	src, err := NewClaudeSource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pane := permissionPane(t, "claude_auto_mode_setup_dialog_real_pane.txt")
	src.capturePane = func(context.Context, string) (string, error) { return pane, nil }
	shown := protocolQuestionOrNil(question.Detect(pane))
	if shown != nil {
		questionID = shown.ID
	}
	const id = "claude:s1"
	src.sessions[id] = claudeSession{
		meta:     protocol.Session{ID: id, State: protocol.StateWaitingInput, Question: shown},
		tmuxName: "agentman-claude-s1",
	}
	closed = &[]string{}
	src.closeDialog = func(_ context.Context, name string) error {
		*closed = append(*closed, name)
		return nil
	}
	return src, questionID, closed
}

// Claude reports a session with a dialog open as waiting, and before this the
// phone said "Needs you" with nothing to show. Now it shows what is open.
func TestAClaudeCodeDialogIsShownOnThePhone(t *testing.T) {
	src, err := NewClaudeSource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	meta := protocol.Session{ID: "claude:s1", State: protocol.StateIdle}
	src.applyPane(&meta, "", permissionPane(t, "claude_auto_mode_setup_dialog_real_pane.txt"))
	if meta.State != protocol.StateWaitingInput || meta.Question == nil {
		t.Fatalf("state %q, question %+v: the dialog is not shown", meta.State, meta.Question)
	}
	if meta.Question.Title != "Teach auto mode about your environment?" ||
		len(meta.Question.Options) != 1 || meta.Question.Options[0].Label != "Close it" {
		t.Fatalf("shown as %+v", meta.Question)
	}
}

func TestClosingAClaudeCodeDialogPressesEsc(t *testing.T) {
	src, questionID, closed := claudeWithDialogOpen(t)
	err := src.Answer(context.Background(), "claude:s1", protocol.QuestionAnswer{
		QuestionID: questionID, OptionKey: question.DialogCloseKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(*closed) != 1 || (*closed)[0] != "agentman-claude-s1" {
		t.Fatalf("Esc pressed in %v, want once in the session's pane", *closed)
	}
}

// Close is the only thing the phone does to a dialog. Its own controls and
// Enter, which would act on them, stay at the keyboard.
func TestAClaudeCodeDialogTakesNoOtherAnswer(t *testing.T) {
	src, questionID, closed := claudeWithDialogOpen(t)
	for _, answer := range []protocol.QuestionAnswer{
		{QuestionID: questionID, OptionKey: "1"},
		{QuestionID: questionID, Text: "undo the mac changes"},
		{QuestionID: questionID, OptionKey: question.DialogCloseKey, Text: "and continue"},
	} {
		if err := src.Answer(context.Background(), "claude:s1", answer); err == nil {
			t.Errorf("answer %+v was accepted", answer)
		}
	}
	if len(*closed) != 0 {
		t.Fatalf("Esc pressed in %v", *closed)
	}
}

// A message from the phone used to be typed straight into the dialog: the
// text went behind it, and Enter went to a dialog whose footer offers "Enter
// to continue". It is refused now, and the refusal says why.
func TestAMessageIsNotTypedIntoAClaudeCodeDialog(t *testing.T) {
	src, _, _ := claudeWithDialogOpen(t)
	_, err := src.Inject(context.Background(), "claude:s1", "undo the mac changes")
	if err == nil || !strings.Contains(err.Error(), "dialog") {
		t.Fatalf("Inject = %v, want a refusal that names the dialog", err)
	}
}
