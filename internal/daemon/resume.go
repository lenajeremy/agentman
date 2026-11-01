package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
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

	// Some agents' sessions take a message as they are (Muse): reopening one
	// only lists it again, and nothing starts.
	if reopened, err := d.registry.Reopen(ctx, sessionID); reopened {
		if err != nil {
			return "", err
		}
		d.resumes.remember(sessionID, sessionID, time.Now())
		return sessionID, nil
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
	// Kiro carries on through its ACP server, as a session started from here
	// does; a pane only where that server cannot run.
	if session.Kind == protocol.KindKiro {
		id, err := resumeKiroACP(ctx, d.registry, session.ID, session.Cwd)
		if err == nil {
			d.resumes.remember(sessionID, id, time.Now())
			return id, nil
		}
		if !errors.Is(err, source.ErrNoKiroACP) {
			return "", err
		}
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

// handoffSession moves a live session to the other side of its Handoff: one
// Agentman runs itself into a pane of its own ("Open in terminal"), or one in
// Agentman's pane back into Agentman ("Take over"). The adapter closes the
// side being left and waits for the agent there to let go of the
// conversation; then the other side opens on it. A side that fails to open
// puts the session back where it was, so a move never leaves it closed.
func (d *Daemon) handoffSession(ctx context.Context, sessionID string) (string, error) {
	var live protocol.Session
	for _, session := range d.snapshot() {
		if session.ID == sessionID {
			live = session
			break
		}
	}
	if live.ID == "" {
		return "", errors.New("daemon: this session is not running")
	}
	handoff, ok := handoffFor(d.registry, live.Kind)
	resume, resumable := source.ResumeArgs(live.Kind, live.NativeID)
	if !ok || !resumable {
		return "", errors.New("daemon: this session cannot be moved")
	}
	agent := handoffAgentName(live.Kind)
	naming := func(native, pane string) (string, string) {
		return d.registry.ResumedSession(live.ID, native, pane)
	}
	var id string
	switch live.Handoff {
	case protocol.HandoffTerminal:
		cwd, err := handoff.ReleaseDirect(ctx, live.ID)
		if err != nil {
			return "", err
		}
		id, err = startResumedSession(ctx, live.Kind, cwd, resume, naming)
		if err != nil {
			if _, back := handoff.ResumeDirect(ctx, live.ID, live.NativeID, cwd); back != nil {
				log.Printf("handoff: %s did not go back into Agentman: %v", live.ID, back)
			}
			return "", fmt.Errorf("daemon: %s did not open in a terminal (%v), so it carries on here", agent, err)
		}
	case protocol.HandoffAgentman:
		cwd, err := handoff.ReleasePane(ctx, live.ID)
		if err != nil {
			return "", err
		}
		id, err = handoff.ResumeDirect(ctx, live.ID, live.NativeID, cwd)
		if err != nil {
			if _, back := startResumedSession(ctx, live.Kind, cwd, resume, naming); back != nil {
				log.Printf("handoff: %s did not go back into its pane: %v", live.ID, back)
			}
			return "", fmt.Errorf("daemon: Agentman could not take %s over (%v), so it is back in its terminal", agent, err)
		}
	default:
		return "", errors.New("daemon: this session cannot be moved")
	}
	// Coming up on the other side, it may look ended for a sweep or two.
	d.resumes.remember(live.ID, id, time.Now())
	return id, nil
}

// handoffAgentName is how a move's errors name the agent.
func handoffAgentName(kind protocol.Kind) string {
	switch kind {
	case protocol.KindKiro:
		return "Kiro"
	case protocol.KindClaude:
		return "Claude"
	default:
		return string(kind)
	}
}

// handoffFor is a variable so a test can move a session without an agent.
var handoffFor = func(registry *source.Registry, kind protocol.Kind) (source.Handoff, bool) {
	return registry.Handoff(kind)
}

// resumeKiroACP is a variable so a test can resume without Kiro.
var resumeKiroACP = func(ctx context.Context, registry *source.Registry, sessionID, cwd string) (string, error) {
	return registry.ResumeKiroACP(ctx, sessionID, cwd)
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
