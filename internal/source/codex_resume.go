package source

import (
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// codexResumeMarker separates the daemon's own pane name from the thread a
// resume pane reopens: agentman-codex-<ms>-<hex>-resume-<thread>.
const codexResumeMarker = "-resume-"

// ResumedSession implements ResumeNamer.
//
// `codex resume <thread>` keeps writing the thread's original rollout, whose
// session_meta is as old as the conversation, and discovery lets a pane claim
// only a rollout started after it, because matching by folder alone could show
// a pane someone else's conversation. A reopened pane therefore never found
// its conversation: the phone was sent to an empty row while the transcript
// sat read-only beside it. The pane now carries the thread in its name, which
// is a match discovery can trust, and the session keeps the pane-keyed id
// every Codex pane has.
func (s *CodexSource) ResumedSession(native, defaultPane string) (string, string) {
	if !validCodexThread(native) {
		return "", ""
	}
	pane := defaultPane + codexResumeMarker + native
	return pane, string(protocol.KindCodex) + ":" + tmuxID(pane)
}

// codexResumedThread reports the thread a resume pane was opened for.
func codexResumedThread(paneName string) (string, bool) {
	_, thread, found := strings.Cut(paneName, codexResumeMarker)
	if !found || !validCodexThread(thread) {
		return "", false
	}
	return thread, true
}

// validCodexThread accepts what Codex uses for thread ids — a UUID — and
// nothing that could not sit in a tmux session name.
func validCodexThread(thread string) bool {
	if thread == "" || len(thread) > 64 {
		return false
	}
	for _, character := range thread {
		isHex := (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') ||
			(character >= 'A' && character <= 'F')
		if !isHex && character != '-' {
			return false
		}
	}
	return true
}
