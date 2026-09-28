package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Past implements History.
//
// Kiro is the tractable case: one metadata file per session, holding the
// working directory and a title it wrote itself, so nothing has to be guessed
// from a path or read out of a transcript.
func (s *KiroSource) Past(ctx context.Context, dir string, limit int) ([]protocol.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return nil, nil
	}
	limit = limitOrDefault(limit)

	found := make([]protocol.Session, 0, limit)
	for _, entry := range newestTranscripts(s.sessionsDir(), ".json", 0) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(found) == limit {
			break
		}
		// A session's family shares a stem; ".json" also matches nothing else
		// Kiro writes, but the lock and transcript must not be read as meta.
		meta, ok := s.readMeta(entry.path)
		if !ok || !underDirectory(meta.Cwd, dir) {
			continue
		}
		id := string(protocol.KindKiro) + ":" + meta.SessionID
		transcript := strings.TrimSuffix(entry.path, ".json") + ".jsonl"
		found = append(found, protocol.Session{
			ID:             id,
			Kind:           protocol.KindKiro,
			NativeID:       meta.SessionID,
			Name:           kiroName(meta),
			Cwd:            meta.Cwd,
			State:          protocol.StateEnded,
			Inject:         protocol.InjectNone,
			StartedAt:      kiroTime(meta.CreatedAt),
			LastActivityAt: entry.modTime,
			Model:          meta.SessionState.RTSModelState.ModelInfo.ModelID,
		})
		s.past.remember(id, transcript)
	}
	return found, nil
}

// Directories implements History.
func (s *KiroSource) Directories(ctx context.Context) ([]protocol.Folder, error) {
	counts := map[string]*protocol.Folder{}
	for _, entry := range newestTranscripts(s.sessionsDir(), ".json", 0) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		meta, ok := s.readMeta(entry.path)
		if !ok || meta.Cwd == "" {
			continue
		}
		path := filepath.Clean(meta.Cwd)
		folder := counts[path]
		if folder == nil {
			folder = &protocol.Folder{Path: path}
			counts[path] = folder
		}
		folder.Agents++
		if entry.modTime > folder.LastActivityAt {
			folder.LastActivityAt = entry.modTime
		}
	}
	folders := make([]protocol.Folder, 0, len(counts))
	for _, folder := range counts {
		folders = append(folders, *folder)
	}
	return folders, nil
}

// kiroTime parses the RFC 3339 stamp Kiro writes into its metadata.
func kiroTime(value string) int64 {
	if value == "" {
		return 0
	}
	when, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return 0
	}
	return when.UnixMilli()
}

// pastKiroSession rebuilds the reading shape for a session Discover cannot
// see, so Page and Follow take one path whether a session is live or ended.
func (s *KiroSource) pastKiroSession(sessionID string) (kiroSession, bool) {
	transcript, ok := s.past.path(sessionID)
	if !ok {
		return kiroSession{}, false
	}
	if _, err := os.Stat(transcript); err != nil {
		// Kiro writes the transcript lazily, so a session that never got a
		// turn has metadata and no file. It is still a session; it simply has
		// nothing to show.
		return kiroSession{}, true
	}
	return kiroSession{transcript: transcript}, true
}
