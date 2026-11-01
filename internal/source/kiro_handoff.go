package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// Moving a Kiro chat between Kiro's ACP server, which Agentman runs, and a
// terminal pane Agentman started.
//
// Kiro lets one process have a chat open at a time: whichever opens it holds
// <id>.lock, and a second one is refused ("Session is active in another
// process") rather than taking it over, from a terminal and over ACP alike.
// So a move closes the side being left, waits until Kiro has let go of the
// chat, and only then does the daemon open the other side; and only between
// turns, since closing mid-turn would cut the turn off. Kiro's own terminal
// app is an ACP client too: the lock is held by the `kiro-cli acp` under it.

// ErrKiroBusy refuses a move while Kiro works or waits on the user.
var ErrKiroBusy = errors.New("source: Kiro is in the middle of a turn. Move it once the turn has ended, or stop the turn first")

// kiroHandoffWait is how long a closed Kiro has to let go of the chat. Closing
// its input, Kiro was seen to exit and drop the lock in 0.4 s.
var kiroHandoffWait = 10 * time.Second

// kiroACPShutdown is how long a closed ACP child has to exit before it is
// killed.
const kiroACPShutdown = 3 * time.Second

// kiroPanesAvailable is a variable so a test can offer a pane without tmux.
var kiroPanesAvailable = tmux.Available

// kiroACPInstalled reports whether `kiro-cli` is here to run over ACP.
var kiroACPInstalled = func() bool {
	_, err := exec.LookPath(kiroACPSpec.binary)
	return err == nil
}

// kiroMoveHold is the longest a moving session is listed with neither side
// having it.
const kiroMoveHold = time.Minute

// kiroMove is a session being moved: listed as it was, but working, until it
// is listed from the other side, as to with toInject, so the apps neither see
// it vanish for a sweep nor see Kiro letting go of it, which looks like the
// chat open in a terminal Agentman cannot type into.
type kiroMove struct {
	meta     protocol.Session
	to       string
	toInject protocol.InjectMode
	from     protocol.InjectMode
	until    time.Time
	// released is set once the side being left has let go. Listed from
	// that side again after it, the move failed and the session was put
	// back.
	released bool
}

func (s *KiroSource) holdMove(meta protocol.Session, to string, toInject protocol.InjectMode) {
	s.movesMu.Lock()
	defer s.movesMu.Unlock()
	if s.moves == nil {
		s.moves = map[string]kiroMove{}
	}
	s.moves[meta.ID] = kiroMove{
		meta: meta, to: to, toInject: toInject, from: meta.Inject, until: time.Now().Add(kiroMoveHold),
	}
}

func (s *KiroSource) releasedMove(sessionID string) {
	s.movesMu.Lock()
	defer s.movesMu.Unlock()
	if move, ok := s.moves[sessionID]; ok {
		move.released = true
		s.moves[sessionID] = move
	}
}

func (s *KiroSource) dropMove(sessionID string) {
	s.movesMu.Lock()
	defer s.movesMu.Unlock()
	delete(s.moves, sessionID)
}

// applyMoves lists each moving session as it was, but working, until it is
// on the other side or put back where it was.
func (s *KiroSource) applyMoves(found []protocol.Session) []protocol.Session {
	s.movesMu.Lock()
	defer s.movesMu.Unlock()
	now := time.Now()
	for id, move := range s.moves {
		at := func(want string) int {
			return slices.IndexFunc(found, func(session protocol.Session) bool { return session.ID == want })
		}
		if arrived := at(move.to); arrived >= 0 && found[arrived].Inject == move.toInject {
			delete(s.moves, id) // on the other side
			continue
		}
		listed := at(id)
		if listed >= 0 && move.released && found[listed].Inject == move.from {
			delete(s.moves, id) // put back
			continue
		}
		if now.After(move.until) {
			delete(s.moves, id)
			continue
		}
		if move.to != id {
			// Kiro letting go, under the chat's own id.
			if early := at(move.to); early >= 0 {
				found = slices.Delete(found, early, early+1)
				listed = at(id)
			}
		}
		held := move.meta
		held.State, held.Handoff, held.Question = protocol.StateBusy, "", nil
		if listed >= 0 {
			found[listed] = held
		} else {
			found = append(found, held)
		}
	}
	return found
}

// applyHandoff says where each session can move: one Agentman runs over ACP
// into a pane, while tmux is here; one in Agentman's pane, once Kiro has the
// chat open in it, over to ACP, while Kiro is here to run it. Sessions in
// the user's own terminals are not offered a move: closing them is not
// Agentman's to do.
func (s *KiroSource) applyHandoff(found []protocol.Session, next map[string]kiroSession) {
	panes, acp := false, false
	panesKnown, acpKnown := false, false
	for i := range found {
		session := &found[i]
		entry := next[session.ID]
		switch {
		case session.Inject == protocol.InjectAPI && session.NativeID != "":
			if !panesKnown {
				panes, panesKnown = kiroPanesAvailable(), true
			}
			if !panes {
				continue
			}
			session.Handoff = protocol.HandoffTerminal
		case entry.tmuxName != "" && entry.held && session.NativeID != "":
			if !acpKnown {
				acp, acpKnown = s.acpAvailable(), true
			}
			if !acp {
				continue
			}
			session.Handoff = protocol.HandoffAgentman
		default:
			continue
		}
		entry.meta.Handoff = session.Handoff
		next[session.ID] = entry
	}
}

// acpAvailable reports whether a session could be run over ACP here.
func (s *KiroSource) acpAvailable() bool {
	return s.acpDial != nil || kiroACPInstalled()
}

// ReleaseDirect implements Handoff: it closes a chat Agentman runs over ACP,
// between turns, so the daemon can open it in a pane, and returns the folder
// it ran in. Messages sent from here on are refused until it is open there.
func (s *KiroSource) ReleaseDirect(ctx context.Context, sessionID string) (string, error) {
	st, ok := s.acpOwned(sessionID)
	if !ok {
		return "", errors.New("source: this Kiro session is not running through Agentman")
	}
	s.mu.RLock()
	last, listed := s.sessions[sessionID]
	s.mu.RUnlock()
	// Out of the table under the same lock that starts turns, so none can
	// start between the check and the close.
	s.acp.mu.Lock()
	if st.busy || st.pending != nil || st.loading || len(st.queued) > 0 {
		s.acp.mu.Unlock()
		return "", ErrKiroBusy
	}
	if s.acp.sessions[st.native] == st {
		delete(s.acp.sessions, st.native)
	}
	if st.idle != nil {
		st.idle.Stop()
	}
	client, native, cwd := st.client, st.native, st.cwd
	s.acp.mu.Unlock()
	if listed {
		s.holdMove(last.meta, sessionID, protocol.InjectTmux)
	}
	if client != nil {
		client.shutdown(ctx, kiroACPShutdown)
	}
	if err := s.awaitChatReleased(ctx, native); err != nil {
		s.dropMove(sessionID)
		return "", err
	}
	s.releasedMove(sessionID)
	return cwd, nil
}

// ReleasePane implements Handoff: it closes the pane Agentman started for a
// Kiro chat, between turns, so the daemon can carry it on over ACP, and
// returns the folder it ran in. Nothing is closed that could not then be
// carried on: Kiro must be here to run over ACP.
func (s *KiroSource) ReleasePane(ctx context.Context, sessionID string) (string, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok || session.tmuxName == "" || !session.held || session.meta.NativeID == "" {
		return "", errors.New("source: this Kiro session is not open in a pane Agentman started")
	}
	if session.meta.State == protocol.StateBusy || session.meta.State == protocol.StateWaitingInput {
		return "", ErrKiroBusy
	}
	if session.meta.Cwd == "" {
		return "", errors.New("source: this session did not record a working directory")
	}
	if !s.acpAvailable() {
		return "", ErrNoKiroACP
	}
	native := session.meta.NativeID
	s.holdMove(session.meta, string(protocol.KindKiro)+":"+native, protocol.InjectAPI)
	kill := s.killPane
	if kill == nil {
		kill = tmux.Kill
	}
	if err := kill(ctx, session.tmuxName); err != nil {
		s.dropMove(sessionID)
		return "", err
	}
	if err := s.awaitChatReleased(ctx, native); err != nil {
		s.dropMove(sessionID)
		return "", err
	}
	s.releasedMove(sessionID)
	return session.meta.Cwd, nil
}

// ResumeDirect implements Handoff: it carries a chat on over ACP after its
// pane closed, under the chat's own id. A session that went by its pane is
// followed there (Session.Previous).
func (s *KiroSource) ResumeDirect(ctx context.Context, sessionID, native, cwd string) (string, error) {
	id := string(protocol.KindKiro) + ":" + native
	previous := ""
	if sessionID != id {
		previous = sessionID
	}
	return s.resumeACP(ctx, id, cwd, previous)
}

var _ Handoff = (*KiroSource)(nil)

// awaitChatReleased waits until no live process holds the chat's lock.
func (s *KiroSource) awaitChatReleased(ctx context.Context, native string) error {
	deadline := time.Now().Add(kiroHandoffWait)
	for {
		if !s.chatHeld(native) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("source: Kiro still has this chat open somewhere else; close it there first")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// chatHeld reports whether a live process holds the chat's lock. A lock left
// by a process that has gone does not hold it: Kiro opens the chat over it.
func (s *KiroSource) chatHeld(native string) bool {
	raw, err := readBoundedFile(filepath.Join(s.sessionsDir(), native+".lock"), 4096)
	if err != nil {
		return false
	}
	var lock kiroLock
	return json.Unmarshal(raw, &lock) == nil && processAlive(lock.PID)
}
