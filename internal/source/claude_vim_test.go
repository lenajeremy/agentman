package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/tmux"
)

func readPaneFixture(t *testing.T, path ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(path...))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestClaudeNormalModeReadsTheFooter(t *testing.T) {
	cases := []struct {
		name string
		pane string
		want bool
	}{
		{"insert", readPaneFixture(t, "testdata", "claude-pane-vim-insert.txt"), false},
		{"normal", readPaneFixture(t, "testdata", "claude-pane-vim-normal.txt"), true},
		// A question has the prompt box's place: not a prompt in any mode.
		{"question", readPaneFixture(t, "..", "question", "testdata", "claude_single_real_pane.txt"), false},
		{"draft of several lines", strings.Join([]string{
			strings.Repeat("─", 60), "❯ first line", "  second line", "  third line", strings.Repeat("─", 60),
			"  ⏵⏵ auto mode on (shift+tab to cycle)",
		}, "\n"), true},
		{"nothing under the box", strings.Join([]string{
			strings.Repeat("─", 60), "❯ ", strings.Repeat("─", 60),
		}, "\n"), false},
	}
	for _, c := range cases {
		if got := claudeNormalMode(c.pane); got != c.want {
			t.Errorf("%s: normal mode = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestClaudeEditorModeFollowsSettings(t *testing.T) {
	home := t.TempDir()
	var mode claudeEditorMode
	if mode.vimOn(home) {
		t.Fatal("no settings file is not Vim")
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(body string, at time.Time) {
		t.Helper()
		if err := os.WriteFile(settings, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(settings, at, at); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	write(`{"model": "opus", "editorMode": "vim"}`, now)
	if !mode.vimOn(home) {
		t.Fatal(`"editorMode": "vim" not read`)
	}
	write(`{"editorMode": "normal"}`, now.Add(time.Second))
	if mode.vimOn(home) {
		t.Fatal("a changed settings file was not reread")
	}
}

// What Agentman types into a Claude whose prompt is in normal mode, recorded
// byte for byte by a pane that only writes down its input.
func TestClaudeInjectReturnsAVimPromptToInsertMode(t *testing.T) {
	if !tmux.Available() {
		t.Skip("tmux not installed")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"editorMode":"vim"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		fixture string
		want    string
	}{
		{"claude-pane-vim-normal.txt", "i\x15/compact\r"},
		{"claude-pane-vim-insert.txt", "\x15/compact\r"},
	} {
		name := "agentman-claude-vim-" + strings.TrimSuffix(strings.TrimPrefix(c.fixture, "claude-pane-vim-"), ".txt")
		out := filepath.Join(t.TempDir(), "typed")
		ctx := context.Background()
		if err := tmux.Launch(ctx, name, home, []string{"sh", "-c", "stty raw -echo; cat > " + out}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tmux.Kill(context.Background(), name) })
		// The pane's shell takes a moment to switch the terminal to raw.
		time.Sleep(300 * time.Millisecond)

		pane := readPaneFixture(t, "testdata", c.fixture)
		s := &ClaudeSource{
			home:        home,
			capturePane: func(context.Context, string) (string, error) { return pane, nil },
			sessions:    map[string]claudeSession{"claude:s1": {tmuxName: name}},
		}
		if _, err := s.Inject(ctx, "claude:s1", "/compact"); err != nil {
			t.Fatalf("%s: %v", c.fixture, err)
		}
		var typed string
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			data, _ := os.ReadFile(out)
			if typed = string(data); strings.HasSuffix(typed, "\r") {
				break
			}
		}
		if typed != c.want {
			t.Errorf("%s: typed %q, want %q", c.fixture, typed, c.want)
		}
	}
}
