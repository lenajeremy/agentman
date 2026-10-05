package source

import (
	"context"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Captured from Codex 0.159.3 on a private tmux server: asked to `touch` a
// file under a read-only sandbox, it asks to run the command. Choosing "No,
// and tell Codex what to do differently" ends the turn ("Conversation
// interrupted") and gives the composer back, where the note goes as the next
// message.

func codexAtApproval(t *testing.T) (*CodexSource, string) {
	t.Helper()
	src, err := NewCodexSource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pane := permissionPane(t, "codex_approval_real_pane.txt")
	src.revealQuestion = func(context.Context, string) (string, error) { return pane, nil }
	shown := src.detectQuestion(context.Background(), "agentman-codex-s1")
	if shown == nil {
		t.Fatal("the approval was not detected")
	}
	const id = "codex:tmux-agentman-codex-s1"
	src.sessions[id] = codexSession{
		meta:     protocol.Session{ID: id, Question: shown},
		tmuxName: "agentman-codex-s1",
	}
	return src, shown.ID
}

func TestCodexsRefusalCanCarryANote(t *testing.T) {
	src, _ := codexAtApproval(t)
	options := src.sessions["codex:tmux-agentman-codex-s1"].meta.Question.Options
	if len(options) != 3 {
		t.Fatalf("got %d options", len(options))
	}
	for i, option := range options {
		want := strings.HasPrefix(option.Label, "No, and tell Codex what to do differently")
		if option.WithText != want {
			t.Errorf("option %d %q: WithText = %v", i+1, option.Label, option.WithText)
		}
	}
}

type refusalCall struct {
	pane, key, note string
	composerBack    func(string) bool
}

func TestANoteOnCodexsRefusalIsSentOnceTheComposerIsBack(t *testing.T) {
	src, questionID := codexAtApproval(t)
	var call refusalCall
	src.refuseWithNote = func(_ context.Context, name, key, note string, composerBack func(string) bool) error {
		call = refusalCall{name, key, note, composerBack}
		return nil
	}
	const note = "use echo instead of touch"
	err := src.Answer(context.Background(), "codex:tmux-agentman-codex-s1", protocol.QuestionAnswer{
		QuestionID: questionID, OptionKey: "3", Text: note,
	})
	if err != nil {
		t.Fatal(err)
	}
	if call.pane != "agentman-codex-s1" || call.key != "3" || call.note != note {
		t.Fatalf("refused with %+v", call)
	}
	if call.composerBack(permissionPane(t, "codex_approval_real_pane.txt")) {
		t.Error("the approval itself was taken for the composer")
	}
	if !call.composerBack(permissionPane(t, "codex_refused_composer_real_pane.txt")) {
		t.Error("the composer Codex returns to after a refusal was not recognised")
	}
	// Codex draws its status line just above the composer while a turn runs.
	working := strings.Replace(permissionPane(t, "codex_refused_composer_real_pane.txt"),
		"› Ask Codex to do anything", "• Working (3s • esc to interrupt)\n\n› Ask Codex to do anything", 1)
	if call.composerBack(working) {
		t.Error("a turn still running was taken for the composer")
	}
}

func TestANoteOnAnotherCodexChoiceIsRefused(t *testing.T) {
	src, questionID := codexAtApproval(t)
	src.refuseWithNote = func(context.Context, string, string, string, func(string) bool) error {
		t.Fatal("a note was sent with a choice that takes none")
		return nil
	}
	err := src.Answer(context.Background(), "codex:tmux-agentman-codex-s1", protocol.QuestionAnswer{
		QuestionID: questionID, OptionKey: "1", Text: "run it twice",
	})
	if err == nil {
		t.Error("a note on \"Yes, proceed\" was accepted")
	}
}
