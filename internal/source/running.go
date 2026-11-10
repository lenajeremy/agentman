package source

import (
	"path/filepath"
	"strings"

	"github.com/lenajeremy/agentman/internal/procinfo"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// conversationOpener is a source that can tell, for one of its sessions,
// whether an agent still has its conversation open, where a pid can't say.
// known is false when it can't tell, and the pid decides.
type conversationOpener interface {
	ConversationOpen(session protocol.Session) (open, known bool)
}

// ConversationOpen reports whether a Codex process holds the session's
// rollout open. Codex's shared app-server keeps every live thread's rollout
// open, as a Codex running alone keeps its own, and a rollout an exited agent
// left is open in nothing. Codex lists such a rollout as idle, and records a
// pid only for a pane, so neither the listing nor a pid says whether a
// conversation in a terminal the user opened still runs.
func (s *CodexSource) ConversationOpen(session protocol.Session) (bool, bool) {
	path, ok := s.rolloutFor(session.ID)
	if (!ok || path == "") && session.NativeID != "" {
		path, ok = s.rolloutFor(string(protocol.KindCodex) + ":" + session.NativeID)
	}
	if !ok || path == "" {
		return false, false
	}
	return heldOpen(path, "codex", listProcesses, openFiles)
}

// heldOpen reports whether a process of that name holds path open, and
// whether it could tell: not where processes can't be read.
func heldOpen(path, name string, list func() ([]procinfo.Process, error),
	files func(int) ([]procinfo.OpenFile, error)) (bool, bool) {
	processes, err := list()
	if err != nil {
		return false, false
	}
	want := resolved(path)
	for _, process := range processes {
		// Homebrew's codex is "codex"; the native binary npm runs on Linux is
		// "codex-x86_64-un…", cut to the kernel's 15 bytes.
		if process.Zombie || !strings.HasPrefix(process.Comm, name) {
			continue
		}
		open, err := files(process.PID)
		if err != nil {
			continue
		}
		for _, file := range open {
			if resolved(file.Path) == want {
				return true, true
			}
		}
	}
	return false, true
}

// resolved is a path with its links followed, as the kernel names an open
// file: /tmp is /private/tmp on a Mac.
func resolved(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}
