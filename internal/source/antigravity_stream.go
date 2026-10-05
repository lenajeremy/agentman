package source

import (
	"fmt"
	"regexp"
	"strings"
)

// agy writes nothing while it thinks.
//
// Claude and Codex append to their transcripts as a reply forms, so following
// the file streams the answer. agy writes each step only once that step is
// finished — Kiro, too, writes a reply only once it is complete — so a reply
// lands as one PLANNER_RESPONSE record, status DONE: measured here at a
// 1,500-word reply landing as a single 24 KB jump thirty seconds after the
// prompt. Its chunk shards and its SQLite conversation are written at the same
// moment, so there is no second file to watch.
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
// Returns "" when the pane shows no reply in progress, which is the common
// case and must not produce an empty message.
//
// The reply being written is whatever follows the latest thing agy drew at
// column zero: the echoed prompt ("> …"), or a tool call ("● Bash(ls)"), after
// which the model writes the reply to that call's result. The reply itself is
// indented by two spaces, so anything else at column zero below that point —
// a tool row's wrapped tail, a permission heading, a tip — is interface.
func antigravityPartialReply(pane string) string {
	lines := strings.Split(strings.ReplaceAll(pane, "\r\n", "\n"), "\n")
	lines = dropAntigravityFooter(lines)

	start := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "> ") || strings.HasPrefix(lines[i], "● ") {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}

	body := lines[start+1:]
	if strings.HasPrefix(lines[start], "> ") {
		// The prompt itself wraps onto indented continuation lines, and ends
		// at the first blank line or at the reasoning summary, whichever
		// comes first.
		for i, line := range body {
			if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "▸") {
				body = body[i:]
				break
			}
			if i == len(body)-1 {
				body = nil
			}
		}
	}

	kept := make([]string, 0, len(body))
	skipNext := false
	for _, line := range body {
		line = strings.TrimRight(line, " \t")
		trimmed := strings.TrimSpace(line)
		if skipNext {
			// The one-line summary agy prints under "Thought for 8s".
			skipNext = false
			if strings.HasSuffix(trimmed, "…") || strings.HasSuffix(trimmed, "...") {
				continue
			}
		}
		switch {
		case trimmed == "":
			kept = append(kept, "")
			continue
		case strings.HasPrefix(trimmed, "▸"):
			// "Thought for 8s" and its collapsed summary are chrome, not the
			// answer; the transcript does not record them either. A queued
			// message is drawn the same way.
			skipNext = true
			continue
		case !strings.HasPrefix(line, " "):
			// Column zero: a tool row's wrapped tail, a heading, a spinner,
			// "└ Tip:". The reply is always indented.
			continue
		case isAntigravitySpinner(trimmed),
			strings.HasPrefix(trimmed, "Tip:"),
			strings.HasPrefix(trimmed, "⎿"),
			strings.HasPrefix(trimmed, "┃"),
			antigravityArtifactNotice.MatchString(trimmed):
			continue
		}
		kept = append(kept, strings.TrimPrefix(line, "  "))
	}

	return strings.Trim(strings.Join(kept, "\n"), " \n")
}

// antigravityArtifactNotice is the line agy right-aligns above the prompt
// while artifacts wait for review: "1 artifact · /artifact to review".
var antigravityArtifactNotice = regexp.MustCompile(`^\d+ artifacts? · /artifact to review$`)

// dropAntigravityFooter removes everything from the composer down: its rules,
// the prompt line, any running-subagent or task rows beneath it, and the
// status line.
//
// The composer is the last line opening with ">" that has a rule directly
// above it and another rule below it; an echoed prompt has the first but
// never the second. Without a composer in view — a panel open over it — only
// the bottom rows are dropped, as before.
func dropAntigravityFooter(lines []string) []string {
	for i := len(lines) - 1; i > 0; i-- {
		if lines[i] != ">" && !strings.HasPrefix(lines[i], "> ") {
			continue
		}
		if !isAntigravityRule(strings.TrimSpace(lines[i-1])) || strings.HasPrefix(lines[i-1], " ") {
			continue
		}
		for _, below := range lines[i+1:] {
			if isAntigravityRule(strings.TrimSpace(below)) && !strings.HasPrefix(below, " ") {
				return lines[:i-1]
			}
		}
	}
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
