package source

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// endedSessions is the sessions the user ended, each with its last activity
// then. An ended session stays off the list until it does something newer.
type endedSessions struct {
	path string
	mu   sync.Mutex
	at   map[string]int64
}

func newEndedSessions(path string) *endedSessions {
	e := &endedSessions{path: path, at: map[string]int64{}}
	if path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(raw, &e.at)
			if e.at == nil {
				e.at = map[string]int64{}
			}
		}
	}
	return e
}

func (e *endedSessions) add(id string, lastActivity int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.at[id] = lastActivity
	e.saveLocked()
}

func (e *endedSessions) forget(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.at[id]; ok {
		delete(e.at, id)
		e.saveLocked()
	}
}

// without is the sessions left to list. One that has done something since it
// was ended is listed again, and forgotten.
func (e *endedSessions) without(sessions []protocol.Session) []protocol.Session {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.at) == 0 {
		return sessions
	}
	kept := sessions[:0:0]
	changed := false
	for _, session := range sessions {
		at, ended := e.at[session.ID]
		active := session.State == protocol.StateBusy || session.State == protocol.StateWaitingInput
		switch {
		case !ended:
			kept = append(kept, session)
		case active || session.LastActivityAt > at:
			delete(e.at, session.ID)
			changed = true
			kept = append(kept, session)
		}
	}
	if changed {
		e.saveLocked()
	}
	return kept
}

func (e *endedSessions) saveLocked() {
	if e.path == "" {
		return
	}
	raw, err := json.Marshal(e.at)
	if err != nil || os.MkdirAll(filepath.Dir(e.path), 0o700) != nil {
		return
	}
	tmp := e.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, e.path)
	}
}
