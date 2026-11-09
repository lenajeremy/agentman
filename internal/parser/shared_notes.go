package parser

import "strings"

// SharedNotesOpen and SharedNotesClose wrap the shared notes Agentman adds to
// the end of a message for an agent that can't read them from a file yet.
const (
	SharedNotesOpen  = "<agentman-shared-notes>"
	SharedNotesClose = "</agentman-shared-notes>"
)

// StripSharedNotes is a message as the person wrote it: the notes Agentman
// added are for the agent, and the chat shows what was typed.
func StripSharedNotes(text string) string {
	start := strings.Index(text, SharedNotesOpen)
	if start < 0 {
		return text
	}
	rest := text[start:]
	end := strings.Index(rest, SharedNotesClose)
	if end < 0 {
		return strings.TrimRight(text[:start], " \t\r\n")
	}
	after := rest[end+len(SharedNotesClose):]
	return strings.TrimSpace(text[:start] + after)
}
