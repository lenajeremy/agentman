package daemon

import "github.com/lenajeremy/agentman/internal/protocol"

// SetDaemonInfo gives the daemon what it says about itself: its version, and
// the releases newer than it. That travels with every session list, so a phone
// learns it on connecting without asking.
func (d *Daemon) SetDaemonInfo(info func() *protocol.DaemonInfo) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.daemonInfo = info
}

// AnnounceDaemonInfo sends the session list again, so phones already
// connected hear about a release found after they joined.
func (d *Daemon) AnnounceDaemonInfo() {
	_ = d.sink.Send(d.sessionsEvent(d.snapshot()))
}

// sessionsEvent is a session list as sent to the phone, with the daemon's
// own details attached.
func (d *Daemon) sessionsEvent(list []protocol.Session) protocol.Event {
	event := protocol.Event{Type: protocol.EvtSessions, Sessions: list}
	d.mu.Lock()
	info := d.daemonInfo
	d.mu.Unlock()
	if info != nil {
		event.Daemon = info()
	}
	return event
}
