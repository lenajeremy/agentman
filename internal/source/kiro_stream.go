package source

import (
	"context"
	"regexp"
	"strings"

	"github.com/lenajeremy/agentman/internal/parser"
)

// Kiro writes a reply to its transcript only once the reply is finished:
// during a 600-word answer the transcript held the prompt and nothing else,
// and a reply stopped part-way left only "Response was interrupted by the
// user". The reply exists on screen meanwhile — Kiro's interface draws on the
// normal screen, so tmux keeps what scrolls off — and that is where the phone
// reads it from, as Antigravity's adapter does.
//
// What comes off the screen is a preview: rendered, reflowed to the pane's
// width, and markdown already turned into text. It is sent under the id the
// finished text will carry (parser.KiroTextID), so when Kiro writes the reply
// it replaces the preview in place. If the message turns out to have been a
// tool call with no text, the preview is withdrawn with an empty text under
// the same id.

// kiroScrollbackLines bounds how much history one capture reads: room for a
// long reply, cheap enough to read every tick of a follow.
const kiroScrollbackLines = 600

// kiroToolRow is the first line of a row Kiro draws for a call or an event
// rather than for the agent's words: "• Write /work/a.txt", "↓ Shell ls",
// "• Task list created", "• Switched to Queue mode".
var kiroToolRow = regexp.MustCompile(`^(Shell|Read|Write|Edit|Grep|Glob|WebFetch|WebSearch|Web fetch|Web search|` +
	`Introspect|Knowledge|Code|AWS|UseAws|Subagent|Summary|Goal|Todo|Task list|Tasks updated|Orchestrat\w*|` +
	`Delegat\w*|Rejected|Cancelled|Switched to|Agent changed|Compact\w*)\b`)

// kiroInterfaceNotice is a row about the interface rather than the turn.
var kiroInterfaceNotice = regexp.MustCompile(`^(Switched to|Agent changed)\b`)

// kiroDiffRow is a line of a write's diff or its summary under a call row.
var kiroDiffRow = regexp.MustCompile(`^(added|removed) \d+ lines?|^\d+[+-]\s`)

// kiroPartialReply extracts the reply Kiro is writing from a pane, or "".
//
// The turn on screen is the user's message — after the last full-width rule,
// up to a blank line — then one block per item, each starting at column 0
// with "•" (or "↓" for a call awaiting approval). The reply being written is
// the last block, when that block is text rather than a call. Only the caller
// knows whether a reply is being written at all; outside one, the last block
// is the finished reply, which the transcript already has.
func kiroPartialReply(pane string) string {
	lines := strings.Split(strings.ReplaceAll(pane, "\r\n", "\n"), "\n")
	lines = dropKiroChrome(lines)

	start := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if isKiroRule(lines[i]) {
			start = i + 1
			break
		}
	}
	body := lines[start:]
	// The user's message, echoed indented, runs to the first blank line.
	for i, line := range body {
		if strings.TrimSpace(line) == "" {
			body = body[i:]
			break
		}
		if strings.HasPrefix(line, "•") || strings.HasPrefix(line, "↓") {
			body = body[i:] // no echo on screen: the turn began above it
			break
		}
		if i == len(body)-1 {
			body = nil
		}
	}

	// The last block, passing over notices about the interface itself
	// ("• Switched to Queue mode"), which Kiro prints wherever it is.
	block, end := -1, len(body)
	for i := len(body) - 1; i >= 0; i-- {
		line := body[i]
		if !strings.HasPrefix(line, "•") && !strings.HasPrefix(line, "↓") {
			continue
		}
		if kiroInterfaceNotice.MatchString(strings.TrimSpace(strings.TrimPrefix(line, "•"))) {
			end = i
			continue
		}
		block = i
		break
	}
	if block < 0 || strings.HasPrefix(body[block], "↓") {
		return ""
	}
	first := strings.TrimSpace(strings.TrimPrefix(body[block], "•"))
	rest := body[block+1 : end]
	if first == "" || kiroToolRow.MatchString(first) {
		return ""
	}
	text := []string{first}
	for _, line := range rest {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "╰") || trimmed == "esc to cancel" || kiroDiffRow.MatchString(trimmed) {
			return "" // a call's parameters or diff: this block is a call
		}
		text = append(text, strings.TrimPrefix(strings.TrimRight(line, " "), "  "))
	}
	return strings.TrimRight(strings.Join(text, "\n"), " \n")
}

// dropKiroChrome removes what Kiro pins below the conversation: the prompt or
// "Kiro is working" line, the status line and its rule, the queue and task
// counters, the thinking spinner and the clipboard hint.
func dropKiroChrome(lines []string) []string {
	end := len(lines)
	for end > 0 {
		line := lines[end-1]
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "",
			strings.HasPrefix(trimmed, "›"),
			kiroStatusLine.MatchString(line),
			isKiroRule(line),
			trimmed == "/copy to clipboard",
			strings.HasPrefix(trimmed, "◇"), strings.HasPrefix(trimmed, "◐"),
			isKiroSpinner(line):
			end--
			continue
		}
		break
	}
	return lines[:end]
}

// isKiroRule reports a full-width rule Kiro draws between messages. A rule in
// the agent's own markdown is indented with the rest of its text.
func isKiroRule(line string) bool {
	if len(line) < 8*len("─") || strings.HasPrefix(line, " ") {
		return false
	}
	for _, r := range line {
		if r != '─' {
			return false
		}
	}
	return true
}

// isKiroSpinner reports the line Kiro animates while the model thinks:
// "ᗣ Thinking... (esc to cancel)". It is unindented and ends with the hint.
func isKiroSpinner(line string) bool {
	return !strings.HasPrefix(line, " ") && strings.HasSuffix(strings.TrimSpace(line), "(esc to cancel)")
}

// streamingReply reads the reply Kiro is writing in a session's pane, with
// the id its finished text will have and a timestamp that sorts it after
// everything the transcript has given so far.
//
// Only while the transcript says a reply is owed — after a prompt, or once
// every call has its result. At any other moment the last text on screen is
// already in the transcript, and sending it again would add it twice.
func (s *KiroSource) streamingReply(
	ctx context.Context, session kiroSession, p *parser.KiroParser,
) (text, id string, ts int64, ok bool) {
	if session.tmuxName == "" || s.captureScrollback == nil || !p.AwaitingReply() {
		return "", "", 0, false
	}
	promptID, texts, nextTs := p.TurnState()
	if promptID == "" {
		return "", "", 0, false
	}
	pane, err := s.captureScrollback(ctx, session.tmuxName, kiroScrollbackLines)
	if err != nil {
		return "", "", 0, false
	}
	partial := kiroPartialReply(pane)
	if partial == "" {
		return "", "", 0, false
	}
	return partial, parser.KiroTextID(promptID, texts+1), nextTs, true
}
