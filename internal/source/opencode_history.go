package source

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"sync"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Past implements History.
//
// OpenCode is the one agent whose history is not on disk in a form we read: it
// lives behind a running `opencode serve`. That server already lists every
// session it has, from every project — the only thing hiding the old ones is
// openCodeIdleWindow, which exists so a stale session does not sit on the
// status board pretending to be live. A folder's history wants exactly what
// that window throws away, so this lists without it.
//
// The consequence is honest and worth stating: with no server running there is
// no OpenCode history, because there is nothing to ask.
func (s *OpenCodeSource) Past(ctx context.Context, dir string, limit int) ([]protocol.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return nil, nil
	}
	limit = limitOrDefault(limit)

	listed, err := s.listEverySession(ctx)
	found := make([]protocol.Session, 0, limit)
	for _, item := range listed.sessions {
		if len(found) == limit {
			break
		}
		directory := item.session.directory()
		if !underDirectory(directory, dir) {
			continue
		}
		id := string(protocol.KindOpenCode) + ":" + item.session.ID
		model := cleanModel(item.session.Model.ID)
		if model == "" {
			model, _ = s.models.get(id)
		}
		found = append(found, protocol.Session{
			ID:             id,
			Kind:           protocol.KindOpenCode,
			NativeID:       item.session.ID,
			Name:           openCodeName(item.session),
			Cwd:            directory,
			State:          protocol.StateEnded,
			Inject:         protocol.InjectNone,
			StartedAt:      item.session.Time.Created,
			LastActivityAt: openCodeUpdated(item.session),
			Model:          model,
		})
		s.rememberPastRoute(id, openCodeSession{
			nativeID: item.session.ID, baseURL: item.base, directory: directory,
		})
	}
	return found, err
}

// Directories implements History.
func (s *OpenCodeSource) Directories(ctx context.Context) ([]protocol.Folder, error) {
	listed, err := s.listEverySession(ctx)
	counts := map[string]*protocol.Folder{}
	for _, item := range listed.sessions {
		directory := item.session.directory()
		if directory == "" {
			continue
		}
		path := filepath.Clean(directory)
		folder := counts[path]
		if folder == nil {
			folder = &protocol.Folder{Path: path}
			counts[path] = folder
		}
		folder.Agents++
		if updated := openCodeUpdated(item.session); updated > folder.LastActivityAt {
			folder.LastActivityAt = updated
		}
	}
	folders := make([]protocol.Folder, 0, len(counts))
	for _, folder := range counts {
		folders = append(folders, *folder)
	}
	return folders, err
}

// openCodeListing is every session every reachable server knows about.
type openCodeListing struct {
	sessions []openCodeListedSession
}

type openCodeListedSession struct {
	session ocSession
	base    string
}

// listEverySession asks every local server for its whole session list.
//
// Several servers commonly list the same session, because one runs per
// project directory and each reports the others' history. The first sighting
// wins: any of them can serve the transcript, and the duplicate would
// otherwise appear twice in one folder.
func (s *OpenCodeSource) listEverySession(ctx context.Context) (openCodeListing, error) {
	bases := s.findServers(ctx)
	var (
		mu       sync.Mutex
		listing  openCodeListing
		failures []error
		wg       sync.WaitGroup
	)
	for _, base := range bases {
		wg.Add(1)
		go func(base string) {
			defer wg.Done()
			var listed []ocSession
			if _, err := s.doAt(ctx, base, http.MethodGet, "/session?limit=200", nil, &listed); err != nil {
				mu.Lock()
				failures = append(failures, fmt.Errorf("%s/session: %w", base, err))
				mu.Unlock()
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, item := range listed {
				listing.sessions = append(listing.sessions, openCodeListedSession{session: item, base: base})
			}
		}(base)
	}
	wg.Wait()

	seen := make(map[string]bool, len(listing.sessions))
	unique := listing.sessions[:0]
	for _, item := range listing.sessions {
		if item.session.ID == "" || seen[item.session.ID] {
			continue
		}
		seen[item.session.ID] = true
		unique = append(unique, item)
	}
	listing.sessions = unique
	sort.Slice(listing.sessions, func(i, j int) bool {
		return openCodeUpdated(listing.sessions[i].session) > openCodeUpdated(listing.sessions[j].session)
	})

	if len(failures) > 0 {
		return listing, fmt.Errorf("source: %v", failures[0])
	}
	return listing, nil
}

// openCodeUpdated is a session's last activity, falling back to its creation.
func openCodeUpdated(item ocSession) int64 {
	if item.Time.Updated != 0 {
		return item.Time.Updated
	}
	return item.Time.Created
}

// rememberPastRoute records which server can serve an ended session.
//
// Unlike the file-backed agents, an OpenCode transcript is not a path: it is a
// server plus an id. That is why this keeps a route rather than using the
// shared pastSessions.
func (s *OpenCodeSource) rememberPastRoute(id string, route openCodeSession) {
	s.pastMu.Lock()
	defer s.pastMu.Unlock()
	if s.pastRoutes == nil {
		s.pastRoutes = map[string]openCodeSession{}
	}
	s.pastRoutes[id] = route
}

// routeFor resolves a session id to the server that can serve it, live or
// ended.
func (s *OpenCodeSource) routeFor(sessionID string) (openCodeSession, bool) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if ok {
		return session, true
	}
	s.pastMu.RLock()
	defer s.pastMu.RUnlock()
	route, ok := s.pastRoutes[sessionID]
	return route, ok
}
