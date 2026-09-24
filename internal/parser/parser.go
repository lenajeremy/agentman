// Package parser normalizes each agent CLI's transcript format into
// protocol.Message.
//
// None of these formats are published APIs, so every field access lives in
// this package. When a CLI release moves something, this is the only place
// that needs to change — which is why `am doctor` asserts these shapes rather
// than letting drift surface as a mysteriously empty feed.
package parser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// A phone screen is a hostile place for a 40KB tool result. Everything the
// parsers surface is clipped to something readable at a glance; the full
// content stays on disk, reachable from a terminal.
//
// Output gets a line budget as well as a character one. It used to get neither
// — every result was flattened through clip, which collapses newlines — and a
// grep result arrived on the phone as one reflowed monospace paragraph, the
// least readable form of the most structured thing an agent produces.
const (
	// One tool result: roughly two screenfuls, which is where scrolling a
	// result stops being reading it.
	PreviewChars = 12000
	PreviewLines = 300
	// One header line: a path, a pattern, a URL.
	SummaryChars = 160
	// A command can be a heredoc'd script, so it keeps its shape. Only the
	// first line reaches the collapsed row.
	CommandChars = 2000
	CommandLines = 40
)

// Parser turns one raw transcript line into zero or more normalized messages.
//
// offset is the line's byte position, used to mint stable IDs for records that
// carry no identifier of their own.
type Parser interface {
	Parse(line string, offset int64) []protocol.Message
}

// ClipBlock truncates without destroying line structure.
//
// Tool output is tabular far more often than it is prose — grep hits, file
// listings, test results, stack traces — and every one of those is unreadable
// once its newlines become spaces. Lines are capped before characters so the
// budget is spent on whole lines, and what was dropped is stated rather than
// left to a bare ellipsis: "… 182 more lines" tells you whether to open a
// terminal, where "…" does not.
func ClipBlock(text string, maxLines, maxChars int) string {
	lines := blockLines(text)
	total := len(lines)
	if total == 0 {
		return ""
	}

	kept := lines
	if len(kept) > maxLines {
		kept = kept[:maxLines]
	}
	out := strings.Join(kept, "\n")

	cut := false
	if runes := []rune(out); len(runes) > maxChars {
		out = string(runes[:maxChars])
		cut = true
		// Drop the partial trailing line rather than ending mid-token, unless
		// it is the only line there is.
		if idx := strings.LastIndexByte(out, '\n'); idx > 0 {
			out = out[:idx]
		}
		kept = strings.Split(out, "\n")
	}

	switch dropped := total - len(kept); {
	case dropped == 1:
		out += "\n… 1 more line"
	case dropped > 1:
		out += fmt.Sprintf("\n… %d more lines", dropped)
	case cut:
		out += "…"
	}
	return out
}

// Control sequences a terminal would act on rather than print: colour and
// cursor moves (CSI), window titles (OSC), and the single-byte escapes.
var ansiEscape = regexp.MustCompile("\x1b(?:\\[[0-9;?]*[ -/]*[@-~]|\\][^\a\x1b]*(?:\a|\x1b\\\\)|[@-_])")

// blockLines normalizes line endings and trims the blank lines at either end,
// which otherwise show up as dead space above and below every result.
//
// It also does the two things a terminal does that a plain string read does
// not: it drops escape sequences, which would otherwise print as `[0;32m`
// litter through any colourized build output, and it honours carriage returns
// by keeping only what a line was last overwritten with — which is the whole
// difference between one progress bar and two hundred of them.
func blockLines(text string) []string {
	text = ansiEscape.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if idx := strings.LastIndexByte(line, '\r'); idx >= 0 {
			line = line[idx+1:]
		}
		lines[i] = strings.TrimRight(line, " \t")
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// clip collapses whitespace and truncates to a readable length. It is for the
// single line a collapsed row shows; use ClipBlock for anything a reader will
// expand.
func clip(text string, maxLen int) string {
	flat := strings.Join(strings.Fields(text), " ")
	if len(flat) <= maxLen {
		return flat
	}
	// Truncate on a rune boundary so the preview never ends mid-character.
	runes := []rune(flat)
	if len(runes) <= maxLen {
		return flat
	}
	return string(runes[:maxLen-1]) + "…"
}

// decode parses a line, reporting failure rather than panicking on a malformed
// record — transcripts are written concurrently and can contain junk.
func decode(line string, target any) bool {
	if line == "" {
		return false
	}
	return json.Unmarshal([]byte(line), target) == nil
}

// boundedMap pairs tool calls with their results without unbounded growth.
//
// The pairing works in both read directions, which is what keeps a tool call
// rendering as a single row rather than two: reading forwards we meet the call
// first and fill in the outcome later; reading backwards (paging through
// history) we meet the result first and attach it when the call shows up.
// Either way this map is the only state involved, and it is capped so a long
// session cannot grow it without bound.
type boundedMap[V any] struct {
	entries map[string]V
	order   []string
	limit   int
}

func newBoundedMap[V any](limit int) *boundedMap[V] {
	return &boundedMap[V]{entries: make(map[string]V, limit), limit: limit}
}

func (m *boundedMap[V]) set(key string, value V) {
	if _, exists := m.entries[key]; !exists {
		if len(m.order) >= m.limit {
			oldest := m.order[0]
			m.order = m.order[1:]
			delete(m.entries, oldest)
		}
		m.order = append(m.order, key)
	}
	m.entries[key] = value
}

func (m *boundedMap[V]) get(key string) (V, bool) {
	v, ok := m.entries[key]
	return v, ok
}

// toolOutcome is a tool result waiting to be attached to its call.
type toolOutcome struct {
	status  protocol.ToolStatus
	preview string
}

// toolCall remembers what a tool invocation looked like so its result can be
// re-emitted complete.
//
// The summary has to be carried, not just the name: the settled row replaces
// the running one by id, so dropping it here makes a finished command lose the
// command itself — which showed up as tool rows that displayed "Bash" with no
// command, seemingly at random, depending only on whether the tool had
// completed yet.
type toolCall struct {
	name    string
	summary string
}
