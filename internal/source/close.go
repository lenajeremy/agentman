package source

import (
	"context"
	"errors"

	"github.com/lenajeremy/agentman/internal/protocol"
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

// EndSession ends a session. One in a pane Agentman opened has the pane closed,
// and the agent process goes with it, which is how a session stops costing
// anything. One with nothing to close (Muse between turns, a chat in Cursor, a
// Codex rollout nothing has open) is taken off the list instead, until it does
// something again. Either way the transcript is untouched, so the session stays
// in its folder's history and can be picked up from there.
func (r *Registry) EndSession(ctx context.Context, sessionID string) error {
	s, err := r.forSession(sessionID)
	if err != nil {
		return err
	}
	session, listed := r.lastSession(s.Kind(), sessionID)
	if closer, ok := s.(Closer); ok {
		if name, ok := closer.TmuxName(sessionID); ok && name != "" {
			if err := tmux.Kill(ctx, name); err != nil {
				return err
			}
			// Its transcript would otherwise list it again as an idle agent.
			if listed {
				r.ended.add(session)
			}
			return nil
		}
	}
	if !listed {
		return errors.New("source: this session isn't running any more")
	}
	if session.State == protocol.StateBusy || session.State == protocol.StateWaitingInput {
		return errors.New("source: it's still working; stop it first, then end it")
	}
	r.ended.add(session)
	return nil
}

// RunningUnreachable reports whether a session's agent is running where
// Agentman can't type into it (a terminal the user opened), listed or not.
// Ended there, it only came off the list, and the agent runs on: what the
// last sweep found, before ended sessions were left out, still has it.
func (r *Registry) RunningUnreachable(sessionID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, sessions := range r.last {
		for _, session := range sessions {
			if session.ID != sessionID && endedKey(session) != sessionID {
				continue
			}
			if session.State == protocol.StateEnded || session.Inject == protocol.InjectTmux || session.Inject == protocol.InjectAPI {
				continue
			}
			return true
		}
	}
	return false
}

// ForgetEnded lists a session again: it is being picked back up.
func (r *Registry) ForgetEnded(sessionID string) {
	r.ended.forget(sessionID)
	// Ended under the conversation's name; asked for by another.
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, sessions := range r.last {
		for _, session := range sessions {
			if session.ID == sessionID {
				r.ended.forget(endedKey(session))
			}
		}
	}
}

// KeepEnded keeps the sessions the user ended in a file, so a restart doesn't
// bring them back.
func (r *Registry) KeepEnded(path string) { r.ended = newEndedSessions(path) }

func (r *Registry) lastSession(kind protocol.Kind, sessionID string) (protocol.Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, session := range r.last[kind] {
		if session.ID == sessionID {
			return session, true
		}
	}
	return protocol.Session{}, false
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
