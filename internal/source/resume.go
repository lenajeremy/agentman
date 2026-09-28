package source

import "github.com/lenajeremy/agentman/internal/protocol"

// ResumeArgs returns the argv tail that reopens one of an agent's own
// sessions, and whether that agent can be resumed by id at all.
//
// Every CLI supports this and every one spells it differently — `--resume`,
// `resume` as a subcommand, `--resume-id`, `--conversation`. Keeping the four
// spellings here rather than at each call site is what lets the phone, the
// daemon and `am <agent> --resume` all mean the same thing, and what makes a
// CLI changing its mind a one-line fix.
//
// The id is the agent's own native session id, never the "<kind>:<id>"
// composite the app uses.
func ResumeArgs(kind protocol.Kind, nativeID string) ([]string, bool) {
	if nativeID == "" {
		return nil, false
	}
	switch kind {
	case protocol.KindClaude:
		return []string{"--resume", nativeID}, true
	case protocol.KindCodex:
		// A subcommand, not a flag, and it must come before any prompt.
		return []string{"resume", nativeID}, true
	case protocol.KindCursorCLI:
		return []string{"--resume", nativeID}, true
	case protocol.KindKiro:
		// runWrap already puts `chat` ahead of this; bare --resume would
		// reopen the most recent session in the directory instead of this one.
		return []string{"--resume-id", nativeID}, true
	case protocol.KindAntigravity:
		return []string{"--conversation", nativeID}, true
	default:
		// OpenCode is deliberately absent. Its sessions live in a running
		// server and are injectable over its API whenever one is up, so there
		// is no dead session to revive — `am opencode` starts the server, and
		// that is a different thing from resuming one conversation. Cursor's
		// IDE sessions have no CLI to resume into at all.
		return nil, false
	}
}

// CanResume reports whether a session of this kind can be reopened by id.
func CanResume(kind protocol.Kind) bool {
	_, ok := ResumeArgs(kind, "x")
	return ok
}
