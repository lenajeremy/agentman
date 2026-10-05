package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// maxRememberedFolderSessions bounds what can be resumed from history.
//
// One page of a folder, a few folders deep. The point is not to hold a
// catalogue: it is that the daemon resumes only sessions it found on disk
// itself, rather than taking a kind and a working directory from the phone
// and launching a process with them.
const maxRememberedFolderSessions = 1000

// folderMemory keeps the sessions a folder listing returned.
//
// Resuming needs a session's kind, native id and working directory, and an
// ended session is by definition absent from discovery — so without this the
// daemon would have to be told those by the phone, which is a request to run
// an arbitrary agent in an arbitrary directory. Remembering what we ourselves
// read off disk keeps the phone naming a session rather than describing one.
type folderMemory struct {
	mu       sync.Mutex
	sessions map[string]protocol.Session
	order    []string
}

func (m *folderMemory) remember(sessions []protocol.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions == nil {
		m.sessions = map[string]protocol.Session{}
	}
	for _, session := range sessions {
		if _, seen := m.sessions[session.ID]; !seen {
			m.order = append(m.order, session.ID)
		}
		m.sessions[session.ID] = session
	}
	for len(m.order) > maxRememberedFolderSessions {
		delete(m.sessions, m.order[0])
		m.order = m.order[1:]
	}
}

func (m *folderMemory) get(id string) (protocol.Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	return session, ok
}

// resumeSession reopens a session in a tmux pane so it can be typed into.
//
// Most sessions the folder filter surfaces are read-only: their process is
// gone, so there is nothing to send to. Every CLI can reopen one by id,
// though, so "read-only" is a state to leave rather than a fact to live with
// — opening one is what starts it.
//
// Already running in a pane Agentman owns: nothing happens, and the same id
// comes back. That is what makes this safe to call on open, without the app
// having to know which case it is in.
func (d *Daemon) resumeSession(ctx context.Context, sessionID string) (string, error) {
	if sessionID == "" {
		return "", errors.New("daemon: a session is required")
	}
	// Already typeable: the pane exists and the agent is in it.
	for _, live := range d.snapshot() {
		if live.ID != sessionID {
			continue
		}
		if live.Inject == protocol.InjectTmux {
			return live.ID, nil
		}
		if live.Inject == protocol.InjectAPI {
			// OpenCode takes prompts over its API; there is no pane to make.
			return live.ID, nil
		}
		// Running, but somewhere Agentman cannot type: someone's own terminal.
		// Reopening it by id would start a second agent on the same
		// conversation beside the first, both writing one transcript.
		if live.State != protocol.StateEnded {
			return "", errors.New("daemon: this session is still running in a terminal on your Mac. " +
				"Reopening it here would start a second copy; reply to it there, or close it there first")
		}
	}

	// Reopened a moment ago and still coming up. The pane answers within a
	// fraction of a second, but the agent in it takes a few more to register,
	// and until discovery sees it the session still looks ended — so a second
	// tap, a second phone, or the screen opened again would start it twice.
	if id, ok := d.resumes.recent(sessionID, time.Now()); ok {
		return id, nil
	}

	session, ok := d.resumable(sessionID)
	if !ok {
		return "", fmt.Errorf("daemon: nothing is known about this session any more")
	}
	resume, ok := source.ResumeArgs(session.Kind, session.NativeID)
	if !ok {
		return "", fmt.Errorf("daemon: %s sessions cannot be reopened by id", session.Kind)
	}
	if session.Cwd == "" {
		return "", errors.New("daemon: this session did not record a working directory")
	}
	naming := func(native, pane string) (string, string) {
		return d.registry.ResumedSession(session.ID, native, pane)
	}
	id, err := startResumedSession(ctx, session.Kind, session.Cwd, resume, naming)
	if err != nil {
		return "", err
	}
	d.resumes.remember(sessionID, id, time.Now())
	return id, nil
}

// resumeGrace is how long a resume that has answered stands in for the session
// it started. Long enough for a cold start to register with discovery, after
// which the live session answers for itself. A resume whose agent died at
// once can be tried again when this runs out.
const resumeGrace = 30 * time.Second

// resumeMemory remembers the resumes that answered within resumeGrace.
// Requests that change a session are serialized per session (actionLock), so
// this is consulted and updated by one resume at a time.
type resumeMemory struct {
	mu      sync.Mutex
	started map[string]resumeStart
}

type resumeStart struct {
	id string
	at time.Time
}

func (m *resumeMemory) recent(sessionID string, now time.Time) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	start, ok := m.started[sessionID]
	if !ok || now.Sub(start.at) >= resumeGrace {
		return "", false
	}
	return start.id, true
}

func (m *resumeMemory) remember(sessionID, id string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started == nil {
		m.started = map[string]resumeStart{}
	}
	for key, start := range m.started {
		if now.Sub(start.at) >= resumeGrace {
			delete(m.started, key)
		}
	}
	m.started[sessionID] = resumeStart{id: id, at: now}
}

// resumable finds a session to reopen, live or remembered from a folder.
func (d *Daemon) resumable(sessionID string) (protocol.Session, bool) {
	for _, live := range d.snapshot() {
		if live.ID == sessionID {
			return live, true
		}
	}
	return d.folders.get(sessionID)
}

// endSession closes the pane a session runs in.
//
// Deliberately not "stop the agent": the transcript survives, the session
// reappears in its folder's history, and resuming brings it back. What ends
// is the cost of leaving it open, which is the whole reason panes were piling
// up in the first place.
func (d *Daemon) endSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("daemon: a session is required")
	}
	return d.registry.EndSession(ctx, sessionID)
}
