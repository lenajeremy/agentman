package parser

import "strings"

// SharedNotesOpen, SharedNotesHeader and SharedNotesClose wrap the shared notes
// Agentman adds to the end of a message for an agent that can't read them from
// a file yet.
const (
	SharedNotesOpen   = "<agentman-shared-notes>"
	SharedNotesHeader = "From Agentman, not the user's message:"
	SharedNotesClose  = "</agentman-shared-notes>"
)

// StripSharedNotes is a message as the person wrote it: the notes Agentman
// added are for the agent, and the chat shows what was typed. Only that block
// goes, at the end and with its header: someone writing about the tag keeps
// their words. A copy Muse cut short has no end to find.
func StripSharedNotes(text string) string {
	start := strings.LastIndex(text, SharedNotesOpen+"\n"+SharedNotesHeader)
	if start < 0 {
		return text
	}
	rest := text[start:]
	if end := strings.LastIndex(rest, SharedNotesClose); end >= 0 &&
		strings.TrimSpace(rest[end+len(SharedNotesClose):]) != "" {
		return text
	}
	return strings.TrimRight(text[:start], " \t\r\n")
}
