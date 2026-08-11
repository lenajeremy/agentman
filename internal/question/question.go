// Package question detects the approval prompts agent CLIs show when they
// need a decision from the user.
//
// This is the state that matters most and the one hooks cannot see. Claude
// Code fires no Notification hook for a permission prompt, and its session
// registry still reports the session as "idle" while it sits blocked — from
// the outside, an agent waiting on a decision looks identical to one that has
// finished. The only place the truth exists is the terminal itself.
//
// So detection reads the pane. That has a real cost — it means only sessions
// launched through the tmux wrapper can be answered remotely — but it is the
// difference between a phone that tells you an agent is stuck and one that
// lets you unstick it.
package question

import (
	"regexp"
	"strings"
)

// Question is a pending decision an agent is blocked on.
type Question struct {
	// Prompt is the question itself, e.g. "Do you want to proceed?".
	Prompt string `json:"prompt"`
	// Title names what is being asked about, e.g. "Bash command".
	Title string `json:"title,omitempty"`
	// Detail is the substance — the command, the file, the diff summary.
	Detail string `json:"detail,omitempty"`
	// Options are the choices, in the order shown.
	Options []Option `json:"options"`
}

// Option is one selectable answer.
type Option struct {
	// Key is what to send to choose this option — the digit shown beside it.
	Key string `json:"key"`
	// Label is the human text of the choice.
	Label string `json:"label"`
	// Selected marks the option the TUI currently has highlighted.
	Selected bool `json:"selected,omitempty"`
}

// optionLine matches " ❯ 1. Yes" or "   2. No", capturing the marker, the
// digit, and the label.
//
// The marker set is deliberately wide because each CLI picked a different
// glyph: Claude Code uses ❯ (U+276F) and Codex uses › (U+203A). Missing one
// is not a cosmetic failure — an unmatched marker line stops being an option
// and gets read as the question instead, which is exactly what happened to
// Codex's first choice before › was added here.
var optionLine = regexp.MustCompile(`^\s*([❯›>»▸▶→*]?)\s*(\d+)[.)]\s+(.*\S)\s*$`)

// footerLine matches the key-hint line under a menu, which ends the block.
var footerLine = regexp.MustCompile(`(?i)^\s*(esc to|press |↑/↓|tab to)`)

// inputLine matches the TUI's own input box, which sits above the transcript
// and has nothing to do with the question. Without this the walk upward for
// context runs straight into whatever the user last typed and reports it as
// the question's heading.
var inputLine = regexp.MustCompile(`^\s*[❯›>]\s`)

// maxScan bounds how much of a pane is examined. A prompt is always at the
// bottom, so there is no reason to walk a long scrollback.
const maxScan = 40

// Detect finds a pending question in captured pane text, if there is one.
//
// Returns nil when the agent is not waiting on anything, which is the common
// case — this runs on every discovery sweep for every tmux-backed session.
func Detect(pane string) *Question {
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	if len(lines) > maxScan {
		lines = lines[len(lines)-maxScan:]
	}

	// Find the last run of consecutive numbered options. Scanning from the
	// bottom matters: a transcript can contain older menus, and only the one
	// at the end is still awaiting an answer.
	last := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if optionLine.MatchString(lines[i]) {
			last = i
			break
		}
	}
	if last == -1 {
		return nil
	}

	// Anything after the options other than blanks or a key-hint footer means
	// this menu is already resolved and scrolled past.
	for _, line := range lines[last+1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || footerLine.MatchString(line) || isRule(trimmed) {
			continue
		}
		return nil
	}

	first := last
	var options []Option
	for i := last; i >= 0; i-- {
		match := optionLine.FindStringSubmatch(lines[i])
		if match == nil {
			break
		}
		first = i
		options = append([]Option{{
			Key:      match[2],
			Label:    cleanLabel(match[3]),
			Selected: match[1] != "",
		}}, options...)
	}

	// A single numbered line is far more likely to be prose ("1. install it")
	// than a menu. Requiring a real choice keeps false positives out.
	if len(options) < 2 {
		return nil
	}
	selected := 0
	for _, option := range options {
		if option.Selected {
			selected++
		}
	}
	// Both supported TUIs draw exactly one active selection. Requiring that
	// marker prevents an assistant response ending in an ordinary numbered
	// list from becoming a fake approval card whose digit is typed into the
	// next prompt.
	if selected != 1 {
		return nil
	}

	q := &Question{Options: options}

	// The question is the nearest non-empty line above the options; the title
	// and detail are what sits above that, up to a rule or a blank run.
	// Kept short on purpose: a menu's heading and detail sit directly above it,
	// so walking further only risks dragging in unrelated transcript.
	var context []string
	bounded := false
	for i := first - 1; i >= 0 && len(context) < 14; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if isRule(trimmed) {
			// A rule means the CLI drew a box around this question, so
			// everything gathered belongs to it.
			bounded = true
			break
		}
		if inputLine.MatchString(lines[i]) {
			break
		}
		if trimmed == "" {
			if len(context) > 0 {
				context = append(context, "")
			}
			continue
		}
		context = append(context, trimmed)
	}
	// context is bottom-up; the first entry is the question.
	if len(context) > 0 {
		q.Prompt = context[0]
	}
	if len(context) > 1 {
		rest := context[1:]
		// Trim leading/trailing blanks left by the walk.
		for len(rest) > 0 && rest[0] == "" {
			rest = rest[1:]
		}
		for len(rest) > 0 && rest[len(rest)-1] == "" {
			rest = rest[:len(rest)-1]
		}
		if len(rest) > 0 {
			body := rest
			// Only claim a heading when the block was actually delimited.
			// Claude Code rules off its prompts, so the topmost line really is
			// a title ("Bash command"); Codex does not, so the same guess
			// there promotes an unrelated line of transcript into a heading.
			if bounded {
				q.Title = rest[len(rest)-1]
				body = rest[:len(rest)-1]
			}
			// Restore top-down order for the detail.
			for i, j := 0, len(body)-1; i < j; i, j = i+1, j-1 {
				body[i], body[j] = body[j], body[i]
			}
			q.Detail = strings.TrimSpace(strings.Join(body, "\n"))
		}
	}
	if q.Prompt == "" {
		q.Prompt = "The agent needs a decision"
	}
	return q
}

// isRule reports whether a line is a box-drawing separator.
func isRule(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	for _, r := range trimmed {
		if r != '─' && r != '━' && r != '-' && r != '═' && r != '·' && r != ' ' {
			return false
		}
	}
	return true
}

// cleanLabel tidies a choice for display on a phone.
//
// Codex pads a trailing description onto the same line ("Keep current model
// (never show again)   Hide future rate limit reminders"). Collapsing all the
// whitespace would fuse the choice and its explanation into one run-on, so the
// run of spaces that separates them is treated as the boundary and only the
// choice itself is kept — the label has to be readable as a button. Two spaces
// is enough of a signal, since a real label never contains a double space.
func cleanLabel(label string) string {
	label = strings.TrimRight(strings.TrimSpace(label), " ")
	if idx := strings.Index(label, "  "); idx > 0 {
		label = label[:idx]
	}
	// Curly quotes come straight from the TUI; keep them, they are the CLI's
	// own wording and rewriting it would misquote the choice being made.
	return strings.Join(strings.Fields(label), " ")
}
