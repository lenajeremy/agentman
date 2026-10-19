package source

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/lenajeremy/agentman/internal/question"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// Antigravity holds a message sent while it works until the turn ends, unless
// its "Queued Messages" setting says to send at once. While it holds some,
// the pane draws each as a line starting "▸", the first followed by a hint
// such as "(alt+m to send immediately)", and the footer reads "Press up to
// edit queued messages". Nothing about the queue reaches the transcript: a
// held message is written there only once it is sent, stamped with that time.
//
// The pane says whether anything is waiting, and what to press to send it;
// its lines are cut to the pane's width. The process's own log has the text
// in full (see antigravity_log.go), so the two together say what is waiting:
//
//	… input_loop.go:106] HandleUserInput called with text: "…"
//	… conversation_manager.go:783] Forwarding user message to conversation <id> (items=1, media=0)
//	… input_loop.go:1255] Forwarding mid-turn user message immediately to conversation <id> (…)
//	… Auto-flush: sending 2 queued input(s) (combinedLength=…)
//
// A message sent while agy is idle is forwarded at once; one sent while it
// works is not, until the turn ends (auto-flush) or the send-now key forwards
// everything mid-turn. Nothing here keeps or passes on any line beyond the
// texts the user is shown as waiting.

var (
	antigravityLogInput = regexp.MustCompile(`\] HandleUserInput called with text: ("(?:[^"\\]|\\.)*")\s*$`)
	antigravityLogFlush = regexp.MustCompile(`\] Auto-flush: sending (\d+) queued input`)
	// antigravitySendHint is the hint after the first held message; its key
	// is agy's prompt.submit_alternate binding, alt+m unless rebound.
	antigravitySendHint = regexp.MustCompile(`\s*\(((?:alt|ctrl)\+[a-z]) to send immediately\)\s*$`)
)

// antigravityPaneQueue reads what the pane shows of agy's held messages:
// whether there are any, their visible text, and the key that sends them now
// in tmux's spelling ("M-m"), empty when the pane names none Agentman presses.
func antigravityPaneQueue(lines []string) (shown bool, texts []string, key string) {
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-3; i-- {
		if strings.Contains(lines[i], "Press up to edit queued messages") {
			shown = true
		}
	}
	if !shown {
		return false, nil, ""
	}
	// Held messages sit just above the prompt box; reading only the bottom of
	// the pane keeps a "▸" in the conversation from being taken for one.
	for _, line := range lines[max(0, len(lines)-24):] {
		text, ok := strings.CutPrefix(strings.TrimSpace(line), "▸ ")
		if !ok {
			continue
		}
		if match := antigravitySendHint.FindStringSubmatchIndex(text); match != nil {
			key = tmuxKeyName(text[match[2]:match[3]])
			text = text[:match[0]]
		}
		if text = strings.TrimSpace(text); text != "" {
			texts = append(texts, text)
		}
	}
	return true, texts, key
}

// tmuxKeyName turns agy's "alt+m" or "ctrl+x" into tmux's "M-m" or "C-x".
func tmuxKeyName(key string) string {
	modifier, letter, ok := strings.Cut(key, "+")
	if !ok || len(letter) != 1 {
		return ""
	}
	switch modifier {
	case "alt":
		return "M-" + letter
	case "ctrl":
		return "C-" + letter
	}
	return ""
}

// antigravityLogQueue replays a log's lines for the messages agy is holding,
// oldest first.
func antigravityLogQueue(text string) []string {
	var waiting []string
	flushing := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.Contains(line, "] HandleUserInput called with text: "):
			match := antigravityLogInput.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			if input, err := strconv.Unquote(match[1]); err == nil && strings.TrimSpace(input) != "" {
				waiting = append(waiting, strings.TrimSpace(input))
			}
		case strings.Contains(line, "] Auto-flush: sending "):
			if match := antigravityLogFlush.FindStringSubmatch(line); match != nil {
				sent, _ := strconv.Atoi(match[1])
				waiting = waiting[min(sent, len(waiting)):]
				flushing = true
			}
		case strings.Contains(line, "] Forwarding mid-turn user message"):
			waiting = nil
		case strings.Contains(line, "] Forwarding user message to conversation "):
			// The forward of a flush, or a message sent while agy was idle,
			// which never waited.
			if flushing {
				flushing = false
			} else if len(waiting) > 0 {
				waiting = waiting[:len(waiting)-1]
			}
		}
	}
	return waiting
}

// antigravityQueued is what a session shows as waiting: the log's full texts
// when it accounts for what the pane shows, the pane's own lines otherwise.
func antigravityQueued(logText string, paneTexts []string) []string {
	logged := antigravityLogQueue(logText)
	if len(paneTexts) == 0 {
		return logged // the pane says something waits but drew no line we know
	}
	// The log can hold inputs that were never messages, such as a slash
	// command; the pane shows exactly what is held. Keep the newest that many.
	if len(logged) > len(paneTexts) {
		logged = logged[len(logged)-len(paneTexts):]
	}
	if len(logged) == len(paneTexts) && len(logged) > 0 {
		return logged
	}
	return paneTexts
}

// SendQueued implements QueueSender for Antigravity: it presses the key agy's
// own hint names, which forwards everything it is holding into the running
// turn now, as at the Mac.
//
// The pane is read again first, and nothing is pressed unless it still holds
// messages and nothing else would take the key: an open question, or text in
// agy's own prompt box, which that key would send instead.
func (s *AntigravitySource) SendQueued(ctx context.Context, sessionID string) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown antigravity session %q", sessionID)
	}
	if session.tmuxName == "" {
		return errors.New("source: only sessions started with `am antigravity` can be told to send now")
	}
	capture := s.capturePane
	if capture == nil {
		capture = tmux.Capture
	}
	pane, err := capture(ctx, session.tmuxName)
	if err != nil {
		return err
	}
	if question.DetectAntigravity(pane) != nil {
		return errors.New("source: answer Antigravity's question first")
	}
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	shown, _, key := antigravityPaneQueue(lines)
	if !shown {
		return errors.New("source: Antigravity isn't holding any messages now")
	}
	if key == "" {
		return errors.New("source: Antigravity shows no key to send them now")
	}
	if !antigravityPromptEmpty(lines) {
		return errors.New("source: Antigravity's message box on the Mac has text in it; send from there")
	}
	return tmux.SendQueued(ctx, session.tmuxName, key)
}

// antigravityPromptEmpty reports whether agy's prompt box, the rows between
// the last two rules at the bottom of the pane, holds nothing but its ">".
// A pane it cannot find the box in counts as not empty.
func antigravityPromptEmpty(lines []string) bool {
	var rules []int
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-12 && len(rules) < 2; i-- {
		if trimmed := strings.TrimSpace(lines[i]); len(trimmed) >= 10 && strings.Trim(trimmed, "─") == "" {
			rules = append(rules, i)
		}
	}
	if len(rules) < 2 {
		return false
	}
	for _, line := range lines[rules[1]+1 : rules[0]] {
		if trimmed := strings.TrimSpace(line); trimmed != "" && trimmed != ">" {
			return false
		}
	}
	return true
}
