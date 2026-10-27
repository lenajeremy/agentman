package source

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/tmux"
)

// Claude Code 2.1.311 with Vim editing on ("editorMode": "vim" in
// ~/.claude/settings.json), captured on a private tmux server:
//
//   - In insert mode the line under the prompt box starts "-- INSERT --"; in
//     normal mode it names no mode at all.
//   - Esc with nothing open leaves the prompt in normal mode, where typed text
//     is taken as commands: "/autocompact" arrived as "utocompact", the "/"
//     swallowed and the "a" switching back to insert.
//   - Esc that closes a panel (/usage, /status) leaves insert mode as it was.

const claudeInsertLabel = "-- INSERT --"

// claudePromptMaxLines bounds how tall a draft in the prompt box is looked
// for: past it, the rule above is something else's.
const claudePromptMaxLines = 20

// paneLock is held while typing into a pane or reading a panel there.
func (s *ClaudeSource) paneLock(tmuxName string) *sync.Mutex {
	lock, _ := s.panes.LoadOrStore(tmuxName, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// typeInto types a message at Claude's prompt, first returning a prompt that
// Vim editing left in normal mode to insert mode. "i" there is harmless when
// the footer was misread: the line is cleared before the message is typed.
// The caller holds paneLock.
func (s *ClaudeSource) typeInto(ctx context.Context, tmuxName, text string) error {
	if s.editor.vimOn(s.home) {
		if pane, err := s.capturePane(ctx, tmuxName); err == nil && claudeNormalMode(pane) {
			return tmux.SendAfter(ctx, tmuxName, "i", text)
		}
	}
	return tmux.Send(ctx, tmuxName, text)
}

// claudeNormalMode reports whether a prompt with Vim editing on is in normal
// mode: the line under the prompt box is on screen and does not say INSERT.
// A picker or dialog in place of the prompt box is not normal mode.
func claudeNormalMode(pane string) bool {
	footer, ok := claudePromptFooter(pane)
	return ok && strings.TrimSpace(footer) != "" && !strings.Contains(footer, claudeInsertLabel)
}

// claudePromptFooter finds Claude's prompt box on screen, and what is under
// it. The box is a rule (the session's name may sit in it), the prompt, a
// line or a few starting "❯", and a plain rule. A question is boxed too, but
// its first line is its title, and its "❯" marks an option further down.
func claudePromptFooter(pane string) (string, bool) {
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	rule := -1
	for index := len(lines) - 1; index >= 0; index-- {
		if isBoxRule(lines[index]) {
			rule = index
			break
		}
	}
	for index := rule - 1; index >= 1 && index >= rule-claudePromptMaxLines; index-- {
		if strings.HasPrefix(lines[index], "❯") && isLabeledRule(lines[index-1]) {
			return strings.Join(lines[rule+1:], "\n"), true
		}
	}
	return "", false
}

// isLabeledRule is a box rule that may carry a label: "──── lab test ─".
func isLabeledRule(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "─") && strings.HasSuffix(trimmed, "─") &&
		strings.Count(trimmed, "─") >= 20
}

// claudeEditorMode reads the editor mode from Claude Code's user settings,
// rereading only when the file changes.
type claudeEditorMode struct {
	mu       sync.Mutex
	path     string
	modified time.Time
	vim      bool
}

func (m *claudeEditorMode) vimOn(home string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	path := filepath.Join(home, ".claude", "settings.json")
	info, err := os.Stat(path)
	if err != nil {
		m.path, m.vim = "", false
		return false
	}
	if path == m.path && info.ModTime().Equal(m.modified) {
		return m.vim
	}
	m.path, m.modified, m.vim = path, info.ModTime(), false
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var settings struct {
		EditorMode string `json:"editorMode"`
	}
	if json.Unmarshal(data, &settings) == nil {
		m.vim = strings.EqualFold(strings.TrimSpace(settings.EditorMode), "vim")
	}
	return m.vim
}
