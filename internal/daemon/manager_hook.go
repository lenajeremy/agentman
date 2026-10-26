package daemon

import (
	"context"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// ManagerHook is Agentman, the manager agent, as the daemon sees it: it takes
// the manager_* requests apps send, and hears when an agent finishes a turn
// or needs the user, the same moments that ring the phone.
//
// Defined here, and met by internal/manager, so the daemon does not depend
// on the manager: am serve wires the two together.
type ManagerHook interface {
	Handles(protocol.RequestType) bool
	Handle(ctx context.Context, req protocol.Request) protocol.Event
	// TurnComplete and NeedsAnswer must not block: they are called from
	// discovery and from hook delivery.
	TurnComplete(session protocol.Session)
	NeedsAnswer(session protocol.Session)
	// SessionsChanged hands over the session list after a discovery sweep
	// that changed it, so Agentman can see a question answered elsewhere or
	// an agent pick up again. It must not block either.
	SessionsChanged(sessions []protocol.Session)
	// On reports whether Agentman can run, having its key. While it can, it
	// speaks for the agents, whose own alerts go only where asked for.
	On() bool
}

// SetManager installs the manager. Without one, manager requests are
// rejected like any other unknown request.
func (d *Daemon) SetManager(manager ManagerHook) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.manager = manager
}

// Sessions is the current session list, as apps receive it.
func (d *Daemon) Sessions() []protocol.Session {
	return d.snapshot()
}

func (d *Daemon) managerHook() ManagerHook {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.manager
}

func (d *Daemon) managerTurnComplete(sessionID string) {
	manager := d.managerHook()
	if manager == nil {
		return
	}
	d.mu.Lock()
	session, ok := d.sessions[sessionID]
	d.mu.Unlock()
	if ok {
		manager.TurnComplete(session)
	}
}

func (d *Daemon) managerSessionsChanged() {
	if manager := d.managerHook(); manager != nil {
		manager.SessionsChanged(d.snapshot())
	}
}

func (d *Daemon) managerNeedsAnswer(session protocol.Session) {
	if manager := d.managerHook(); manager != nil && session.Question != nil {
		manager.NeedsAnswer(session)
	}
}
