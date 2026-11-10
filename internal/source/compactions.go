package source

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Compactions says how many times an agent has summarized its conversation
// away. Anything told to it by message before then may be gone. Read when
// sending, not on a sweep: it reads the whole transcript.

// CompactionCounter is implemented by adapters that can tell.
type CompactionCounter interface {
	Compactions(ctx context.Context, sessionID string) (int, bool)
}

// Compactions is a session's count, and whether its agent can tell.
func (r *Registry) Compactions(ctx context.Context, sessionID string) (int, bool) {
	s, err := r.forSession(sessionID)
	if err != nil {
		return 0, false
	}
	counter, ok := s.(CompactionCounter)
	if !ok {
		return 0, false
	}
	return counter.Compactions(ctx, sessionID)
}

// countInFile counts every marker in the one file glob finds.
func countInFile(glob string, markers ...string) (int, bool) {
	paths, err := filepath.Glob(glob)
	if err != nil || len(paths) == 0 {
		return 0, false
	}
	raw, err := os.ReadFile(paths[0])
	if err != nil {
		return 0, false
	}
	total := 0
	for _, marker := range markers {
		total += bytes.Count(raw, []byte(marker))
	}
	return total, true
}

// Compactions counts Muse's records of one, automatic and asked for.
func (s *MuseSource) Compactions(_ context.Context, sessionID string) (int, bool) {
	native, ok := strings.CutPrefix(sessionID, string(protocol.KindMuse)+":")
	if !ok || native == "" || strings.ContainsAny(native, `/\*?[`) {
		return 0, false
	}
	data := s.museHome()
	if data == "" {
		return 0, false
	}
	return countInFile(filepath.Join(data, "sessions", "*", "*", "*", native, "session.jsonl"),
		`"payload_type":"context.compaction.installed"`, `"terminal_outcome":"compacted"`)
}

// Compactions counts the tool catalogue Cursor writes back into a chat's
// transcript when it starts over from a summary. It appears nowhere else.
func (s *CursorCLISource) Compactions(_ context.Context, sessionID string) (int, bool) {
	native, ok := strings.CutPrefix(sessionID, string(protocol.KindCursorCLI)+":")
	if !ok || native == "" || strings.ContainsAny(native, `/\*?[`) {
		return 0, false
	}
	return countInFile(filepath.Join(s.home, ".cursor", "projects", "*", "agent-transcripts", native, native+".jsonl"),
		`<available_subagent_types>`)
}
