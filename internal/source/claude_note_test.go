package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
)

// permissionPane is one of the captures of Claude Code 2.1.289 asking to run
// `mkdir probe-dir`, taken as "No" was focused, opened with Tab, and given a
// note.
func permissionPane(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "question", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Claude's "No" can say what to do instead: Tab on it opens "No, and tell
// Claude what to do differently". The app offers a note on exactly the
// options marked WithText.
func TestClaudesNoOnAPermissionPromptCanCarryANote(t *testing.T) {
	found := question.Detect(permissionPane(t, "claude_permission_real_pane.txt"))
	if found == nil {
		t.Fatal("no question")
	}
	options := protocolQuestion(found).Options
	for i, option := range options {
		if want := option.Label == "No"; option.WithText != want {
			t.Errorf("option %d %q: WithText = %v", i+1, option.Label, option.WithText)
		}
	}
}

// noteCall records what Answer asked the terminal to do.
type noteCall struct {
	pane     string
	distance int
	note     string
	focused  func(string) bool
	amending func(string) bool
	typed    func(string) bool
}

func claudeAtPermissionPrompt(t *testing.T) (*ClaudeSource, *noteCall, string) {
	t.Helper()
	src, err := NewClaudeSource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pane := permissionPane(t, "claude_permission_real_pane.txt")
	src.capturePane = func(context.Context, string) (string, error) { return pane, nil }
	shown := protocolQuestion(question.Detect(pane))
	const id = "claude:s1"
	src.sessions[id] = claudeSession{
		meta:     protocol.Session{ID: id, Question: shown},
		tmuxName: "agentman-claude-s1",
	}
	call := &noteCall{}
	src.answerWithNote = func(_ context.Context, name string, distance int, note string,
		focused, amending, typed func(string) bool) error {
		*call = noteCall{name, distance, note, focused, amending, typed}
		return nil
	}
	return src, call, shown.ID
}

// The note is delivered the way Claude takes one: focus "No", press Tab,
// type the note into the line that opens, press Enter. Each step is checked
// against what is on screen before the next.
func TestANoteOnClaudesNoIsTypedIntoTheLineTabOpens(t *testing.T) {
	src, call, questionID := claudeAtPermissionPrompt(t)
	const note = "name it probe-two instead"
	err := src.Answer(context.Background(), "claude:s1", protocol.QuestionAnswer{
		QuestionID: questionID, OptionKey: "4", Text: note,
	})
	if err != nil {
		t.Fatal(err)
	}
	if call.pane != "agentman-claude-s1" || call.distance != 3 || call.note != note {
		t.Fatalf("asked to move %d and type %q into %q", call.distance, call.note, call.pane)
	}
	for _, check := range []struct {
		name    string
		test    func(string) bool
		fixture string
		want    bool
	}{
		{"No focused", call.focused, "claude_permission_no_focused_real_pane.txt", true},
		{"Yes still focused", call.focused, "claude_permission_real_pane.txt", false},
		{"the note line open", call.amending, "claude_permission_amend_open_real_pane.txt", true},
		{"the note line not open yet", call.amending, "claude_permission_no_focused_real_pane.txt", false},
		{"the note typed", call.typed, "claude_permission_amend_typed_real_pane.txt", true},
		{"nothing typed yet", call.typed, "claude_permission_amend_open_real_pane.txt", false},
	} {
		if got := check.test(permissionPane(t, check.fixture)); got != check.want {
			t.Errorf("%s: check says %v", check.name, got)
		}
	}
}

// Only the choice Claude lets amend takes a note.
func TestANoteOnAnotherChoiceIsRefused(t *testing.T) {
	src, call, questionID := claudeAtPermissionPrompt(t)
	err := src.Answer(context.Background(), "claude:s1", protocol.QuestionAnswer{
		QuestionID: questionID, OptionKey: "1", Text: "and be quick",
	})
	if err == nil || call.pane != "" {
		t.Fatalf("a note on Yes was accepted (err %v, call %+v)", err, call)
	}
}
