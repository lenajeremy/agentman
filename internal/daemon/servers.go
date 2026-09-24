package daemon

import (
	"context"
	"slices"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/servers"
)

// serverScanInterval is how often listening ports are checked. Slower than
// discovery on purpose: a scan runs lsof, and a server appearing on the phone
// a few seconds after it starts is soon enough.
const serverScanInterval = 3 * time.Second

// ServerWatcher finds the servers each session has started, and can stop one.
type ServerWatcher interface {
	Update(ctx context.Context, owners []servers.Owner) (map[string][]protocol.Server, error)
	Stop(ctx context.Context, owners []servers.Owner, sessionID string, port int) error
}

// ServerSharer turns a local port into a preview link.
type ServerSharer interface {
	Open(ctx context.Context, port int) (string, error)
	Close(port int)
	Links() map[int]string
	Retain(live map[int]bool)
	CloseAll()
}

// SetServers enables server detection and, when sharer is non-nil, sharing.
// A daemon running without a relay still detects servers for the terminal;
// it just has nothing to share them through.
func (d *Daemon) SetServers(watcher ServerWatcher, sharer ServerSharer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.serverWatcher = watcher
	d.sharer = sharer
}

func (d *Daemon) watchServers(ctx context.Context) {
	ticker := time.NewTicker(d.serverScanInterval)
	defer ticker.Stop()
	for {
		d.scanServers(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// scanServers refreshes every session's server list and reports the sessions
// whose list changed.
func (d *Daemon) scanServers(ctx context.Context) {
	d.mu.Lock()
	watcher, sharer := d.serverWatcher, d.sharer
	sessions := make([]protocol.Session, 0, len(d.sessions))
	for _, session := range d.sessions {
		sessions = append(sessions, session)
	}
	d.mu.Unlock()
	if watcher == nil {
		return
	}

	found, err := watcher.Update(ctx, servers.OwnersOf(sessions))
	if err != nil {
		// Keep the last lists: one failed lsof should not make every server
		// vanish from the phone and reappear three seconds later.
		return
	}

	var links map[int]string
	if sharer != nil {
		links = sharer.Links()
	}
	live := map[int]bool{}
	for _, list := range found {
		for i := range list {
			live[list[i].Port] = true
			list[i].Link = links[list[i].Port]
		}
	}
	// A link must die with its server. Otherwise it would keep pointing at
	// whatever listens on that port next, which could be something else
	// entirely.
	if sharer != nil {
		sharer.Retain(live)
	}

	d.mu.Lock()
	d.serverLists = found
	var changed []protocol.Session
	for id, session := range d.sessions {
		next := found[id]
		if protocol.SameServers(session.Servers, next) {
			continue
		}
		session.Servers = next
		d.sessions[id] = session
		changed = append(changed, session)
	}
	d.mu.Unlock()

	for i := range changed {
		_ = d.sink.Send(protocol.Event{Type: protocol.EvtSessionUpdate, Session: &changed[i]})
	}
}

// openServer shares one of a session's servers and answers with its link.
func (d *Daemon) openServer(ctx context.Context, sessionID string, port int) protocol.Event {
	d.mu.Lock()
	sharer := d.sharer
	session, known := d.sessions[sessionID]
	d.mu.Unlock()

	fail := func(message string) protocol.Event {
		return protocol.Event{Type: protocol.EvtError, SessionID: sessionID, Port: port, Error: message}
	}
	if sharer == nil {
		return fail("this Mac is running without a relay, so it cannot share servers")
	}
	// Only a port the watcher attributed to this session can be shared. A
	// paired phone is trusted with the agents, but not with choosing arbitrary
	// ports on the machine to put on the internet.
	if !known || !slices.ContainsFunc(session.Servers, func(s protocol.Server) bool { return s.Port == port }) {
		return fail("that server is no longer running")
	}

	link, err := sharer.Open(ctx, port)
	if err != nil {
		return fail(err.Error())
	}
	d.setServerLink(sessionID, port, link)
	return protocol.Event{Type: protocol.EvtServerOpened, SessionID: sessionID, Port: port, Link: link}
}

// stopServer ends the process listening on a port.
//
// Only a port this session is already reporting may be stopped, the same rule
// open_server follows: a paired phone is trusted with the agents, not with
// naming arbitrary processes on the machine to kill. Any share is withdrawn
// first, so no link is left pointing at a port that is about to close.
func (d *Daemon) stopServer(ctx context.Context, sessionID string, port int) protocol.Event {
	d.mu.Lock()
	watcher, sharer := d.serverWatcher, d.sharer
	sessions := make([]protocol.Session, 0, len(d.sessions))
	for _, session := range d.sessions {
		sessions = append(sessions, session)
	}
	session, known := d.sessions[sessionID]
	d.mu.Unlock()

	fail := func(message string) protocol.Event {
		return protocol.Event{Type: protocol.EvtError, SessionID: sessionID, Port: port, Error: message}
	}
	if watcher == nil {
		return fail("this Mac is not watching for servers")
	}
	if !known || !slices.ContainsFunc(session.Servers, func(s protocol.Server) bool { return s.Port == port }) {
		return fail("that server is no longer running")
	}
	if sharer != nil {
		sharer.Close(port)
	}
	if err := watcher.Stop(ctx, servers.OwnersOf(sessions), sessionID, port); err != nil {
		return fail(err.Error())
	}
	// Report it gone now rather than waiting for the next sweep, so the row
	// does not linger for a few seconds after the tap.
	d.dropServer(sessionID, port)
	return protocol.Event{Type: protocol.EvtServerStopped, SessionID: sessionID, Port: port}
}

// dropServer removes a stopped server from the session and tells every device.
func (d *Daemon) dropServer(sessionID string, port int) {
	d.mu.Lock()
	session, known := d.sessions[sessionID]
	if !known {
		d.mu.Unlock()
		return
	}
	remaining := make([]protocol.Server, 0, len(session.Servers))
	for _, server := range session.Servers {
		if server.Port != port {
			remaining = append(remaining, server)
		}
	}
	session.Servers = remaining
	d.sessions[sessionID] = session
	d.serverLists[sessionID] = remaining
	d.mu.Unlock()
	_ = d.sink.Send(protocol.Event{Type: protocol.EvtSessionUpdate, Session: &session})
}

// closeServer stops sharing a port and tells every device it is private again.
func (d *Daemon) closeServer(sessionID string, port int) {
	d.mu.Lock()
	sharer := d.sharer
	session, known := d.sessions[sessionID]
	d.mu.Unlock()
	if sharer == nil || !known ||
		!slices.ContainsFunc(session.Servers, func(s protocol.Server) bool { return s.Port == port }) {
		return
	}
	sharer.Close(port)
	d.setServerLink(sessionID, port, "")
}

// setServerLink records a link change and pushes it to every device, so a
// second phone sees the server is shared without waiting for the next scan.
func (d *Daemon) setServerLink(sessionID string, port int, link string) {
	d.mu.Lock()
	session, known := d.sessions[sessionID]
	if !known {
		d.mu.Unlock()
		return
	}
	updated := slices.Clone(session.Servers)
	for i := range updated {
		if updated[i].Port == port {
			updated[i].Link = link
		}
	}
	session.Servers = updated
	d.sessions[sessionID] = session
	d.serverLists[sessionID] = updated
	d.mu.Unlock()
	_ = d.sink.Send(protocol.Event{Type: protocol.EvtSessionUpdate, Session: &session})
}
