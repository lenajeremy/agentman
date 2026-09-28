package source

import (
	"context"
	"fmt"

	"github.com/lenajeremy/agentman/internal/tmux"
)

// Closer is implemented by adapters whose sessions run in a tmux pane.
//
// Opening a read-only session starts a pane to resume it, and panes would
// otherwise accumulate for the rest of the login session — one per agent you
// ever glanced at. Being able to close one is what makes opening them freely
// reasonable.
type Closer interface {
	// TmuxName returns the pane hosting a session, and whether there is one.
	// A session running in a terminal the user opened themselves has none:
	// that pane is theirs, and closing it is not ours to do.
	TmuxName(sessionID string) (string, bool)
}

// EndSession closes the tmux pane hosting a session.
//
// The agent process goes with the pane, which is the point: this is how a
// session stops costing anything. The transcript is untouched, so the session
// reappears in its folder's history and can be resumed again.
func (r *Registry) EndSession(ctx context.Context, sessionID string) error {
	s, err := r.forSession(sessionID)
	if err != nil {
		return err
	}
	closer, ok := s.(Closer)
	if !ok {
		return fmt.Errorf("source: %s sessions do not run in a pane", s.Kind())
	}
	name, ok := closer.TmuxName(sessionID)
	if !ok || name == "" {
		return fmt.Errorf("source: this session is not running in a pane Agentman started")
	}
	return tmux.Kill(ctx, name)
}

// PaneFor reports the tmux pane hosting a session, if the daemon can close it.
func (r *Registry) PaneFor(sessionID string) (string, bool) {
	s, err := r.forSession(sessionID)
	if err != nil {
		return "", false
	}
	closer, ok := s.(Closer)
	if !ok {
		return "", false
	}
	return closer.TmuxName(sessionID)
}

// TmuxName implements Closer.
func (s *ClaudeSource) TmuxName(sessionID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	return session.tmuxName, ok && session.tmuxName != ""
}

// TmuxName implements Closer.
func (s *CodexSource) TmuxName(sessionID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	return session.tmuxName, ok && session.tmuxName != ""
}

// TmuxName implements Closer.
func (s *KiroSource) TmuxName(sessionID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	return session.tmuxName, ok && session.tmuxName != ""
}

// TmuxName implements Closer.
func (s *AntigravitySource) TmuxName(sessionID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	return session.tmuxName, ok && session.tmuxName != ""
}

// TmuxName implements Closer.
func (s *CursorCLISource) TmuxName(sessionID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	return session.pane, ok && session.pane != ""
}

// Compile-time proof that every tmux-backed adapter can be closed. OpenCode
// and the Cursor IDE are absent on purpose: neither runs in a pane we own.
var (
	_ Closer = (*ClaudeSource)(nil)
	_ Closer = (*CodexSource)(nil)
	_ Closer = (*KiroSource)(nil)
	_ Closer = (*AntigravitySource)(nil)
	_ Closer = (*CursorCLISource)(nil)

	_ History = (*ClaudeSource)(nil)
	_ History = (*CodexSource)(nil)
	_ History = (*KiroSource)(nil)
	_ History = (*AntigravitySource)(nil)
	_ History = (*CursorCLISource)(nil)
	_ History = (*OpenCodeSource)(nil)
)
