package source

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// kiroScreen is a Kiro approval menu that answers keys the way Kiro 2.27.1
// does: arrows move the cursor, Tab on "No" opens the box for a reason,
// Escape in the box goes back. It records every key and every typed text.
type kiroScreen struct {
	mu      sync.Mutex
	focus   int
	editor  bool
	frozen  bool // ignores the arrows, as a pane mid-redraw might
	noTab   bool // ignores Tab
	keys    []string
	typed   []string
	options []string
}

func newKiroScreen() *kiroScreen {
	return &kiroScreen{options: []string{"Yes, single permission", "Trust, always allow in this session", "No (Tab to edit)"}}
}

func (k *kiroScreen) capture(context.Context, string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.editor {
		return "↓ Shell rm -rf build\n──────\n shell requires approval · Modify request\n ›  add your feedback...\n──────\n esc to close\n", nil
	}
	var b strings.Builder
	b.WriteString("↓ Shell rm -rf build\n──────\n shell requires approval\n")
	for i, option := range k.options {
		mark := "  "
		if i == k.focus {
			mark = "❯ "
		}
		b.WriteString(" " + mark + option + "\n")
	}
	b.WriteString("──────\n esc to close · ↑↓ to navigate · ↵ to select · Tab to edit\n")
	return b.String(), nil
}

func (k *kiroScreen) press(_ context.Context, _ string, keys ...string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, key := range keys {
		k.keys = append(k.keys, key)
		switch {
		case key == "Down" && !k.frozen && !k.editor:
			k.focus = min(k.focus+1, len(k.options)-1)
		case key == "Up" && !k.frozen && !k.editor:
			k.focus = max(k.focus-1, 0)
		case key == "Tab" && !k.noTab && !k.editor && k.focus == len(k.options)-1:
			k.editor = true
		case key == "Escape" && k.editor:
			k.editor, k.focus = false, 0
		}
	}
	return nil
}

func (k *kiroScreen) send(_ context.Context, _ string, text string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.typed = append(k.typed, text)
	return nil
}

func kiroWithScreen(t *testing.T, screen *kiroScreen) (*KiroSource, protocol.Session) {
	t.Helper()
	home := t.TempDir()
	pid := os.Getpid()
	writeKiroSession(t, home, "s", "/work", pid, kiroLinePrompt, kiroLineCall)
	pane := tmux.Session{Name: "agentman-kiro-1-a", PanePID: pid, Cwd: "/work"}
	s := newTestKiro(t, home, fmt.Sprintf("%d 1 kiro-cli-chat\n", pid), pane)
	s.capturePane, s.sendKeys, s.sendText = screen.capture, screen.press, screen.send
	return s, discoverKiro(t, s)["kiro:tmux-agentman-kiro-1-a"]
}

// On the phone the refusal carries a note, as Tab on "No" does in Kiro; the
// other choices stay one tap. The trust options have no refusal to note.
func TestKiroRefusalOffersANote(t *testing.T) {
	_, session := kiroWithScreen(t, newKiroScreen())
	q := session.Question
	if q == nil || len(q.Options) != 3 {
		t.Fatalf("question = %+v", q)
	}
	for i, option := range q.Options {
		if option.WithText != (i == 2) {
			t.Errorf("%q WithText = %v", option.Label, option.WithText)
		}
	}
	trust := kiroQuestionFromPane(t, "↓ Shell ls -la\n──────\n shell requires approval · trust options\n ❯ Full command    ls -la\n"+
		"   Base command    ls *\n   Entire tool\n──────\n esc to close · ↑↓ to navigate · ↵ to select · Tab to edit\n")
	for _, option := range trust.Options {
		if option.WithText {
			t.Errorf("trust option %q offers a note", option.Label)
		}
	}
}

func kiroQuestionFromPane(t *testing.T, pane string) *protocol.Question {
	t.Helper()
	s := newTestKiro(t, t.TempDir(), "")
	found := s.detector(context.Background(), "", "kiro:s")(pane)
	if found == nil {
		t.Fatal("no question")
	}
	return kiroQuestion(found)
}

// "No" with a note: the cursor goes to "No" and is seen there, Tab opens the
// box for the reason and it is seen open, and only then is the note typed and
// sent.
func TestKiroRefusalWithANote(t *testing.T) {
	screen := newKiroScreen()
	s, session := kiroWithScreen(t, screen)
	err := s.Answer(context.Background(), session.ID, protocol.QuestionAnswer{
		QuestionID: session.Question.ID, OptionKey: "3", Text: "  use git clean -n first  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(screen.keys, " "); got != "Down Down Tab" {
		t.Errorf("keys = %q, want the cursor moved to No and Tab", got)
	}
	if len(screen.typed) != 1 || screen.typed[0] != "use git clean -n first" {
		t.Errorf("typed = %q", screen.typed)
	}
}

// Every check stops the answer before the next key. Nothing is typed unless
// the box for the reason is open: typed into the menu, a note would be keys.
func TestKiroRefusalWithANoteStopsWhenTheScreenDisagrees(t *testing.T) {
	t.Run("a choice that takes no note", func(t *testing.T) {
		screen := newKiroScreen()
		s, session := kiroWithScreen(t, screen)
		err := s.Answer(context.Background(), session.ID, protocol.QuestionAnswer{
			QuestionID: session.Question.ID, OptionKey: "1", Text: "only this once"})
		if err == nil || len(screen.keys) != 0 || len(screen.typed) != 0 {
			t.Errorf("err %v, keys %q, typed %q", err, screen.keys, screen.typed)
		}
	})
	t.Run("a stale question", func(t *testing.T) {
		screen := newKiroScreen()
		s, session := kiroWithScreen(t, screen)
		err := s.Answer(context.Background(), session.ID, protocol.QuestionAnswer{
			QuestionID: "old", OptionKey: "3", Text: "no"})
		if err == nil || len(screen.keys) != 0 {
			t.Errorf("err %v, keys %q", err, screen.keys)
		}
	})
	t.Run("the cursor does not move", func(t *testing.T) {
		screen := newKiroScreen()
		s, session := kiroWithScreen(t, screen)
		screen.frozen = true
		err := s.Answer(context.Background(), session.ID, protocol.QuestionAnswer{
			QuestionID: session.Question.ID, OptionKey: "3", Text: "no"})
		if err == nil || strings.Contains(strings.Join(screen.keys, " "), "Tab") || len(screen.typed) != 0 {
			t.Errorf("err %v, keys %q, typed %q", err, screen.keys, screen.typed)
		}
	})
	t.Run("the box does not open", func(t *testing.T) {
		screen := newKiroScreen()
		s, session := kiroWithScreen(t, screen)
		screen.noTab = true
		err := s.Answer(context.Background(), session.ID, protocol.QuestionAnswer{
			QuestionID: session.Question.ID, OptionKey: "3", Text: "no"})
		if err == nil || len(screen.typed) != 0 {
			t.Errorf("err %v, typed %q", err, screen.typed)
		}
	})
}

// With the box already open at the Mac, its one choice goes back to the menu
// and a note is the reason.
func TestKiroReasonBoxAnswers(t *testing.T) {
	screen := newKiroScreen()
	screen.focus, screen.editor = 2, true
	s, session := kiroWithScreen(t, screen)
	if session.Question == nil || !session.Question.Custom {
		t.Fatalf("question = %+v", session.Question)
	}
	if err := s.Answer(context.Background(), session.ID, protocol.QuestionAnswer{
		QuestionID: session.Question.ID, Text: "use git clean -n first"}); err != nil {
		t.Fatal(err)
	}
	if len(screen.typed) != 1 || screen.typed[0] != "use git clean -n first" {
		t.Errorf("typed = %q", screen.typed)
	}
	if err := s.Answer(context.Background(), session.ID, protocol.QuestionAnswer{
		QuestionID: session.Question.ID, OptionKey: "1"}); err != nil {
		t.Fatal(err)
	}
	if len(screen.keys) != 1 || screen.keys[0] != "Escape" {
		t.Errorf("keys = %q, want Escape", screen.keys)
	}
}
