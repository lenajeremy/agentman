package source

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// Past implements History.
//
// Kiro is the tractable case: one metadata file per session, holding the
// working directory and a title it wrote itself, so nothing has to be guessed
// from a path or read out of a transcript.
//
// Not every file family is a past session, though; see kiroHistoryKeeps.
func (s *KiroSource) Past(ctx context.Context, dir string, limit int) ([]protocol.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return nil, nil
	}
	limit = limitOrDefault(limit)

	running := s.kiroRunning(ctx)
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
		base := strings.TrimSuffix(entry.path, ".json")
		if !kiroHistoryKeeps(base, meta) {
			continue
		}
		// A running session is listed by Discover, live. Listing it here too
		// showed it twice in its folder, once as ended — and an ended session
		// is one the phone offers to reopen, which would start a second Kiro
		// on a conversation that already has one.
		if running(base) {
			continue
		}
		id := string(protocol.KindKiro) + ":" + meta.SessionID
		transcript := base + ".jsonl"
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
		if !ok || meta.Cwd == "" || !kiroHistoryKeeps(strings.TrimSuffix(entry.path, ".json"), meta) {
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

// kiroHistoryKeeps reports whether a session file family is a conversation
// worth listing in its folder.
//
// Two kinds are not:
//
//   - A session nobody wrote in. Kiro creates one the moment it starts, before
//     anything is typed, and every launch or /chat switch that went no further
//     left one behind: no title, no transcript, listed under the folder's
//     name with nothing in it.
//   - A subagent's session. Each subagent Kiro starts writes a session of its
//     own, titled with the task it was given, and nothing in it points back to
//     the conversation that started it. What marks one is what it lacks: no
//     agent name, which every session the user talked to records with its
//     first turn, and no input history, which Kiro's interface keeps for every
//     session someone typed into. "session_created_reason" would be the
//     obvious signal, but Kiro 2.27 writes "subagent" there for every session
//     except a /rewind fork.
func kiroHistoryKeeps(base string, meta kiroMeta) bool {
	info, err := os.Stat(base + ".jsonl")
	empty := err != nil || info.Size() == 0
	if strings.TrimSpace(meta.Title) == "" && empty {
		return false
	}
	return empty || !kiroIsSubagentSession(base, meta)
}

// kiroRunning returns a test for whether a session's lock is held by a Kiro
// process that is still running — the rule Discover applies to list it live.
// The process table is read once, and only if some lock names a live pid.
func (s *KiroSource) kiroRunning(ctx context.Context) func(base string) bool {
	var (
		processes *tmux.ProcessTree
		read      bool
	)
	return func(base string) bool {
		raw, err := readBoundedFile(base+".lock", 4096)
		if err != nil {
			return false
		}
		var lock kiroLock
		if json.Unmarshal(raw, &lock) != nil || !processAlive(lock.PID) {
			return false
		}
		if !read && s.snapshotProcesses != nil {
			processes, _ = s.snapshotProcesses(ctx)
			read = true
		}
		// A crash leaves its lock behind and the pid is in time reused; a pid
		// now running something else does not make the session live.
		command := processes.Command(lock.PID)
		return command == "" || isKiroCommand(command)
	}
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
