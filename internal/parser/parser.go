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
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// A phone screen is a hostile place for a 40KB tool result. Everything the
// parsers surface is clipped to something readable at a glance; the full
// content stays on disk, reachable from a terminal.
const (
	PreviewChars = 400
	SummaryChars = 160
)

// Parser turns one raw transcript line into zero or more normalized messages.
//
// offset is the line's byte position, used to mint stable IDs for records that
// carry no identifier of their own.
type Parser interface {
	Parse(line string, offset int64) []protocol.Message
}

// clip collapses whitespace and truncates to a readable length.
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
