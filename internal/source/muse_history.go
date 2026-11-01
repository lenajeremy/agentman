package source

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Muse sessions from before: every one Muse has kept, by folder, from
// session/list, so the apps can list them under their folder and continue
// one. Any of them can take a message (Agentman takes it for the turn), so
// they are shown as quiet rather than ended.

// musePastScan bounds how many sessions one listing reads.
const musePastScan = 1000

// Past implements History.
func (s *MuseSource) Past(ctx context.Context, dir string, limit int) ([]protocol.Session, error) {
	if !s.host.isUp() {
		return nil, nil
	}
	dir = filepath.Clean(dir)
	var past []protocol.Session
	for _, session := range s.allSessions(ctx) {
		if !museUnder(session.WorkspaceRoot, dir) {
			continue
		}
		past = append(past, museToSession(session, false))
	}
	sort.Slice(past, func(i, j int) bool { return past[i].LastActivityAt > past[j].LastActivityAt })
	if limit > 0 && len(past) > limit {
		past = past[:limit]
	}
	return past, nil
}

// Directories implements History.
func (s *MuseSource) Directories(ctx context.Context) ([]protocol.Folder, error) {
	if !s.host.isUp() {
		return nil, nil
	}
	folders := map[string]*protocol.Folder{}
	for _, session := range s.allSessions(ctx) {
		if session.WorkspaceRoot == "" {
			continue
		}
		path := filepath.Clean(session.WorkspaceRoot)
		folder := folders[path]
		if folder == nil {
			folder = &protocol.Folder{Path: path}
			folders[path] = folder
		}
		folder.Agents++
		if session.Status == "running" {
			folder.Running++
		}
		if at := max(museMillis(session.UpdatedAt), museMillis(session.LastActivity)); at > folder.LastActivityAt {
			folder.LastActivityAt = at
		}
	}
	list := make([]protocol.Folder, 0, len(folders))
	for _, folder := range folders {
		list = append(list, *folder)
	}
	return list, nil
}

// allSessions pages through session/list: the root sessions Muse has kept,
// newest first, up to musePastScan.
func (s *MuseSource) allSessions(ctx context.Context) []museSession {
	var sessions []museSession
	var cursor *string
	for len(sessions) < musePastScan {
		params := map[string]any{"limit": 200}
		if cursor != nil {
			params["cursor"] = *cursor
		}
		var page museSessionList
		if err := s.host.call(ctx, "session/list", params, &page); err != nil {
			break
		}
		for _, session := range page.Sessions {
			if session.Kind == "" || session.Kind == "root" {
				sessions = append(sessions, session)
			}
		}
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Sessions) == 0 {
			break
		}
		cursor = page.NextCursor
	}
	return sessions
}

// museUnder reports path being dir or inside it.
func museUnder(path, dir string) bool {
	if path == "" {
		return false
	}
	path = filepath.Clean(path)
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}
