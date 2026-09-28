package source

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// codexHistoryScanFiles bounds how many rollouts one Past call inspects.
//
// Codex files its rollouts by date, not by project, so finding the ones that
// belong to a directory means reading headers until enough match. The headers
// are cached per file, so this ceiling only bites on a cold first call over a
// very long history.
const codexHistoryScanFiles = 4000

// Past implements History.
//
// Unlike Claude, Codex keeps no per-project directory: rollouts are filed by
// date, and the working directory is only in each file's session_meta header.
// Headers are therefore read newest-first until the limit is filled, which the
// existing rollout cache makes cheap on every call after the first.
func (s *CodexSource) Past(ctx context.Context, dir string, limit int) ([]protocol.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return nil, nil
	}
	limit = limitOrDefault(limit)

	rollouts := s.allRollouts()
	found := make([]protocol.Session, 0, limit)
	seen := make(map[string]bool, limit)

	for _, rollout := range rollouts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(found) == limit {
			break
		}
		info, err := os.Stat(rollout.path)
		if err != nil {
			continue
		}
		meta, err := s.cachedCodexMeta(rollout.path, info)
		if err != nil {
			continue
		}
		if !underDirectory(meta.Payload.Cwd, dir) {
			continue
		}
		// A subagent's rollout is part of another session's work, not a
		// session anyone opened. Discover leaves them out; so does history.
		if meta.isSubagent() {
			continue
		}
		threadID := meta.threadID()
		if threadID == "" || seen[threadID] {
			continue
		}
		seen[threadID] = true

		id := string(protocol.KindCodex) + ":" + threadID
		model, cached := s.models.get(id)
		if !cached {
			model = modelFromTranscript(rollout.path, codexModelOf)
			s.models.put(id, model)
		}

		started := parseCodexTime(meta.Payload.Timestamp)
		if started == 0 {
			started = rollout.modTime
		}
		found = append(found, protocol.Session{
			ID:             id,
			Kind:           protocol.KindCodex,
			NativeID:       threadID,
			Name:           historyName(codexRolloutPrompt(rollout.path), meta.Payload.Cwd),
			Cwd:            meta.Payload.Cwd,
			State:          protocol.StateEnded,
			Inject:         protocol.InjectNone,
			StartedAt:      started,
			LastActivityAt: rollout.modTime,
			Model:          model,
		})

		s.pastMu.Lock()
		s.past[id] = rollout.path
		s.pastMu.Unlock()
	}
	return found, nil
}

// Directories implements History.
//
// Every rollout has to be opened to learn its directory, because Codex files
// them by date. Only the header is read, and the rollout cache keeps it, so
// the cost falls to a stat per file after the first call.
func (s *CodexSource) Directories(ctx context.Context) ([]protocol.Folder, error) {
	counts := map[string]*protocol.Folder{}
	seen := map[string]bool{}
	for _, rollout := range s.allRollouts() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := os.Stat(rollout.path)
		if err != nil {
			continue
		}
		meta, err := s.cachedCodexMeta(rollout.path, info)
		if err != nil || meta.Payload.Cwd == "" || meta.isSubagent() {
			continue
		}
		threadID := meta.threadID()
		if threadID == "" || seen[threadID] {
			continue
		}
		seen[threadID] = true

		path := filepath.Clean(meta.Payload.Cwd)
		folder := counts[path]
		if folder == nil {
			folder = &protocol.Folder{Path: path}
			counts[path] = folder
		}
		folder.Agents++
		if rollout.modTime > folder.LastActivityAt {
			folder.LastActivityAt = rollout.modTime
		}
	}
	folders := make([]protocol.Folder, 0, len(counts))
	for _, folder := range counts {
		folders = append(folders, *folder)
	}
	return folders, nil
}

// allRollouts lists every rollout file under the sessions root, newest first.
func (s *CodexSource) allRollouts() []historyEntry {
	root := s.sessionsDir()
	found := make([]historyEntry, 0, 256)
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			// A day directory removed mid-walk is not a reason to abandon the
			// rest of the history.
			return nil //nolint:nilerr // skip unreadable entries
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		found = append(found, historyEntry{path: path, modTime: info.ModTime().UnixMilli()})
		if len(found) >= codexHistoryScanFiles {
			return fs.SkipAll
		}
		return nil
	})
	sort.Slice(found, func(i, j int) bool { return found[i].modTime > found[j].modTime })
	return found
}

// rolloutFor resolves a session id to its rollout, live or ended.
//
// A live pane discovered before its first turn has an empty transcript, which
// is a known session with nothing to read — distinct from an unknown one, so
// the boolean and the path answer separately.
func (s *CodexSource) rolloutFor(sessionID string) (string, bool) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if ok {
		return session.transcript, true
	}
	return s.pastRollout(sessionID)
}

// pastRollout resolves a session Past found and Discover cannot see.
func (s *CodexSource) pastRollout(sessionID string) (string, bool) {
	s.pastMu.RLock()
	defer s.pastMu.RUnlock()
	path, ok := s.past[sessionID]
	return path, ok
}

// codexRolloutPrompt reads the first thing the person typed in a rollout.
//
// Codex opens a rollout with machinery a person did not write: a developer
// message carrying the sandbox policy, then the project's AGENTS.md sent under
// the user role. The completed-item event is the reliable marker of a real
// turn, so that is read first, and the raw user message is only a fallback for
// older rollouts that predate it.
func codexRolloutPrompt(path string) string {
	var prompt, fallback string
	scanHead(path, func(line []byte) bool {
		var record struct {
			Type    string `json:"type"`
			Payload struct {
				Type string `json:"type"`
				Role string `json:"role"`
				Item struct {
					Type    string `json:"type"`
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"item"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &record) != nil {
			return false
		}
		if record.Type == "event_msg" && record.Payload.Type == "item_completed" &&
			record.Payload.Item.Type == "UserMessage" {
			for _, part := range record.Payload.Item.Content {
				if text := usablePrompt(part.Text); text != "" {
					prompt = text
					return true
				}
			}
		}
		if fallback == "" && record.Type == "response_item" &&
			record.Payload.Type == "message" && record.Payload.Role == "user" {
			for _, part := range record.Payload.Content {
				if text := usableCodexPrompt(part.Text); text != "" {
					fallback = text
					break
				}
			}
		}
		return false
	})
	if prompt != "" {
		return prompt
	}
	return fallback
}

// usableCodexPrompt rejects the repository instructions Codex sends as the
// first user message of every session.
func usableCodexPrompt(text string) string {
	if strings.HasPrefix(strings.TrimSpace(text), "# AGENTS.md instructions") {
		return ""
	}
	return usablePrompt(text)
}
