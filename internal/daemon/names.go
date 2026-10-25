package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// The names the user gives sessions. A session's ID is the agent's and never
// changes; a name is only what it is called on screen, and it is applied as
// the session is discovered, so the phone, the Mac, notifications and
// Agentman all see the same one. Kept in names.json, so a restart keeps them.

const (
	maxSessionName  = 80
	maxSessionNames = 500
)

type sessionName struct {
	Name      string `json:"name"`
	UpdatedAt int64  `json:"updatedAt"`
}

// sessionNames is the user's names for sessions, by session ID.
type sessionNames struct {
	mu    sync.Mutex
	path  string
	names map[string]sessionName
}

func loadSessionNames(path string) *sessionNames {
	n := &sessionNames{path: path, names: map[string]sessionName{}}
	if path == "" {
		return n
	}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &n.names)
	}
	return n
}

// apply names a session as the user named it, keeping the agent's own name
// beside it.
func (n *sessionNames) apply(session protocol.Session) protocol.Session {
	if n == nil {
		return session
	}
	n.mu.Lock()
	custom, ok := n.names[session.ID]
	n.mu.Unlock()
	if !ok || custom.Name == "" || custom.Name == session.Name {
		return session
	}
	session.OriginalName = session.Name
	session.Name = custom.Name
	return session
}

// set names a session, or takes its name away when name is empty.
func (n *sessionNames) set(id, name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if name == "" {
		delete(n.names, id)
	} else {
		n.names[id] = sessionName{Name: name, UpdatedAt: time.Now().UnixMilli()}
	}
	// Sessions come and go; the names of the oldest are let go first.
	if len(n.names) > maxSessionNames {
		ids := make([]string, 0, len(n.names))
		for id := range n.names {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return n.names[ids[i]].UpdatedAt < n.names[ids[j]].UpdatedAt })
		for _, old := range ids[:len(ids)-maxSessionNames] {
			delete(n.names, old)
		}
	}
	if n.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(n.names, "", "  ")
	if err != nil {
		return err
	}
	tmp := n.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, n.path)
}

// cleanSessionName is a name as it is kept: on one line, its spaces
// collapsed, within a length.
func cleanSessionName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if containsTerminalControl(name) {
		return "", errors.New("a name can't hold control characters")
	}
	if utf8.RuneCountInString(name) > maxSessionName {
		return "", errors.New("a name is at most 80 characters")
	}
	return name, nil
}

// SetNamesFile keeps the user's names for sessions in path.
func (d *Daemon) SetNamesFile(path string) {
	names := loadSessionNames(path)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.names = names
}

// renameSession gives a session a display name, or its own back, and tells
// every app at once rather than at the next sweep.
func (d *Daemon) renameSession(req protocol.Request) protocol.Event {
	name, err := cleanSessionName(req.Text)
	if err != nil {
		return protocol.Event{Type: protocol.EvtError, SessionID: req.SessionID, Error: err.Error()}
	}
	d.mu.Lock()
	session, known := d.sessions[req.SessionID]
	names := d.names
	d.mu.Unlock()
	if !known {
		return protocol.Event{Type: protocol.EvtError, SessionID: req.SessionID, Error: "no such session"}
	}
	if names == nil {
		names = loadSessionNames("")
		d.mu.Lock()
		d.names = names
		d.mu.Unlock()
	}
	own := session.Name
	if session.OriginalName != "" {
		own = session.OriginalName
	}
	if name == own {
		name = ""
	}
	if err := names.set(req.SessionID, name); err != nil {
		return protocol.Event{Type: protocol.EvtError, SessionID: req.SessionID, Error: "couldn't save the name: " + err.Error()}
	}
	session.Name, session.OriginalName = own, ""
	session = names.apply(session)

	d.mu.Lock()
	if current, ok := d.sessions[req.SessionID]; ok {
		current.Name, current.OriginalName = session.Name, session.OriginalName
		d.sessions[req.SessionID] = current
		session = current
	}
	d.mu.Unlock()
	update := session
	_ = d.sink.Send(protocol.Event{Type: protocol.EvtSessionUpdate, Session: &update})
	d.managerSessionsChanged()
	return protocol.Event{Type: protocol.EvtSessionUpdate, Session: &update}
}
