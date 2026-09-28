package source

import (
	"fmt"
	"strings"
)

// Antigravity is the one agent that writes nothing while it thinks.
//
// Claude, Codex and Kiro all append to their transcripts as a reply forms, so
// following the file streams the answer. agy buffers the whole turn and
// writes one PLANNER_RESPONSE record, status DONE, when it is finished —
// measured here at a 1,500-word reply landing as a single 24 KB jump thirty
// seconds after the prompt. Its chunk shards and its SQLite conversation are
// written at the same moment, so there is no second file to watch.
//
// The reply does exist somewhere while it is being written: the terminal. agy
// renders to the normal screen rather than the alternate one, so tmux keeps
// the scrolled-off part in its history and the whole partial answer can be
// read back.
//
// What comes out of that is a preview, not the transcript. It is wrapped to
// the pane's width, its markdown has already been rendered to text, and any
// of it older than the scrollback is gone. That is the trade: thirty seconds
// of nothing, or thirty seconds of the answer appearing as it is written,
// replaced by the authoritative text the moment the record lands. The
// provisional message carries the id the finished step will use, so the
// replacement is the same row changing rather than a second one arriving.

// antigravityStreamID names the provisional message for a step.
//
// The same id the finished record will carry, so the row updates in place
// rather than a second one arriving beneath it.
func antigravityStreamID(step int) string {
	return fmt.Sprintf("s%06d", step)
}

// antigravityScrollbackLines bounds how much history one capture reads.
//
// A long reply runs to a few hundred wrapped lines. This is generous enough
// for that and small enough that capturing it every second is cheap.
const antigravityScrollbackLines = 600

// antigravityPartialReply extracts the reply being written from a pane.
//
// Returns "" when the pane shows no turn in progress, which is the common
// case and must not produce an empty message.
func antigravityPartialReply(pane string) string {
	lines := strings.Split(strings.ReplaceAll(pane, "\r\n", "\n"), "\n")
	lines = dropAntigravityFooter(lines)

	// The turn starts at the last echoed prompt: an unindented "> " at column
	// zero. The composer at the bottom is a bare ">" with nothing after it,
	// and has already been dropped with the footer.
	start := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "> ") {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}

	// The prompt itself wraps onto indented continuation lines, and ends at
	// the first blank line or at the reasoning summary, whichever comes
	// first.
	body := lines[start+1:]
	for i, line := range body {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "▸") {
			body = body[i:]
			break
		}
		if i == len(body)-1 {
			body = nil
		}
	}

	kept := make([]string, 0, len(body))
	skipNext := false
	for _, line := range body {
		trimmed := strings.TrimSpace(line)
		if skipNext {
			// The one-line summary agy prints under "Thought for 8s".
			skipNext = false
			if strings.HasSuffix(trimmed, "…") || strings.HasSuffix(trimmed, "...") {
				continue
			}
		}
		switch {
		case strings.HasPrefix(trimmed, "▸"):
			// "Thought for 8s" and its collapsed summary are chrome, not the
			// answer; the transcript does not record them either.
			skipNext = true
			continue
		case isAntigravitySpinner(trimmed):
			continue
		case strings.HasPrefix(trimmed, "Tip:"), strings.HasPrefix(trimmed, "└ Tip:"):
			continue
		}
		kept = append(kept, strings.TrimPrefix(line, "  "))
	}

	return strings.TrimRight(strings.Join(kept, "\n"), " \n")
}

// dropAntigravityFooter removes the composer and status bar pinned below the
// conversation: a rule, an empty prompt, a rule, and the shortcut line.
func dropAntigravityFooter(lines []string) []string {
	end := len(lines)
	for end > 0 {
		trimmed := strings.TrimSpace(lines[end-1])
		if trimmed == "" || trimmed == ">" || isAntigravityRule(trimmed) ||
			strings.Contains(trimmed, "esc to cancel") ||
			strings.Contains(trimmed, "? for shortcuts") {
			end--
			continue
		}
		break
	}
	return lines[:end]
}

// isAntigravityRule reports a horizontal rule drawn by the interface.
//
// Only an unindented full-width one. A rule inside the reply is indented with
// everything else and is the agent's own markdown, which belongs in the text.
func isAntigravityRule(trimmed string) bool {
	if len(trimmed) < 8 {
		return false
	}
	for _, r := range trimmed {
		if r != '─' {
			return false
		}
	}
	return true
}

// isAntigravitySpinner reports the in-progress line ("⣷ Generating…").
func isAntigravitySpinner(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	first := []rune(trimmed)[0]
	return first >= '⠀' && first <= '⣿'
}
