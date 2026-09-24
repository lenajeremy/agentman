package daemon

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// A session's agent can read anywhere on the machine; the phone cannot.
//
// Workspace reads are confined to the session's own directory, which is right
// for browsing but wrong for the thing agents actually do with images: a
// screenshot is written to a temp directory, not into the repo, so the one
// file you most want to see is the one file that rule excludes.
//
// Widening the daemon to serve any path — rooting it at the home directory,
// say — would turn a phone token into read access to ~/.ssh, ~/.aws and every
// browser profile on the machine. That is ambient authority, and the relay is
// public.
//
// This is the narrower rule: the phone may read a file only if the agent
// already opened it in that session. The set is built from the transcript, so
// the phone can never name a path the agent did not name first, and it grants
// nothing that was not already flowing to the phone — a file the agent read is
// a file whose contents already travelled as tool output.
const (
	// Per session. A long session names far fewer distinct files than this,
	// and the cap is what stops a runaway agent from growing the set forever.
	maxSeenPaths = 512
)

type seenPaths struct {
	mu       sync.Mutex
	sessions map[string]*pathSet
}

// pathSet is a bounded set with first-in-first-out eviction. Recency is not
// worth tracking: the question is only ever whether this session opened this
// file, and the oldest entry is the one least likely to still be asked for.
type pathSet struct {
	members map[string]struct{}
	order   []string
}

func newSeenPaths() *seenPaths {
	return &seenPaths{sessions: map[string]*pathSet{}}
}

// record adds every absolute file path a batch of messages names.
func (s *seenPaths) record(sessionID string, messages []protocol.Message) {
	if s == nil {
		return
	}
	var found []string
	for _, message := range messages {
		if message.Tool == nil {
			continue
		}
		if path := toolPath(message.Tool.Summary); path != "" {
			found = append(found, path)
		}
	}
	if len(found) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	set, ok := s.sessions[sessionID]
	if !ok {
		set = &pathSet{members: map[string]struct{}{}}
		s.sessions[sessionID] = set
	}
	for _, path := range found {
		if _, known := set.members[path]; known {
			continue
		}
		set.members[path] = struct{}{}
		set.order = append(set.order, path)
		if len(set.order) > maxSeenPaths {
			delete(set.members, set.order[0])
			set.order = set.order[1:]
		}
	}
}

// allows reports whether this session's agent opened this exact path.
func (s *seenPaths) allows(sessionID, path string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	set, ok := s.sessions[sessionID]
	if !ok {
		return false
	}
	_, member := set.members[path]
	return member
}

// forget drops a session's set when the session goes away.
func (s *seenPaths) forget(sessionID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

// toolPath extracts the absolute path a tool call names, or "" when it names
// something else.
//
// Deliberately strict. A tool summary is a command as often as it is a path,
// and "open /tmp/a.png && curl evil" ends in something path-shaped without
// being a file anyone opened. Only a whole summary that is one absolute path,
// with nothing in it that belongs to a shell, counts.
func toolPath(summary string) string {
	first, _, _ := strings.Cut(summary, "\n")
	first = strings.TrimSpace(first)
	if !strings.HasPrefix(first, "/") || len(first) > 4096 {
		return ""
	}
	if strings.ContainsAny(first, " \t\"'`$&|;<>()*?[]\\") {
		return ""
	}
	// A path that needs cleaning is not one an agent reported; treat any
	// difference as a sign this is not the literal file that was opened.
	if filepath.Clean(first) != first {
		return ""
	}
	return first
}
