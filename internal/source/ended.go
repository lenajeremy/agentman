package source

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

const (
	// endedGrace is how long after being ended a session's last writes don't
	// count as something new: closing a terminal flushes its transcript.
	endedGrace = 15 * time.Second
	// endedKept is how long an ended session is remembered.
	endedKept = 30 * 24 * time.Hour
)

// endedKey names a session by the conversation, which outlives where it runs:
// one in a pane is listed under the pane until the pane closes, then under its
// own id.
func endedKey(session protocol.Session) string {
	if session.NativeID != "" && session.Kind != "" {
		return string(session.Kind) + ":" + session.NativeID
	}
	return session.ID
}

// endedSessions is the sessions the user ended, each with the time from which
// activity counts as new. An ended session stays off the list until then.
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
	e.pruneLocked()
	return e
}

func (e *endedSessions) pruneLocked() {
	oldest := time.Now().Add(-endedKept).UnixMilli()
	for id, at := range e.at {
		if at < oldest {
			delete(e.at, id)
		}
	}
}

func (e *endedSessions) add(session protocol.Session) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pruneLocked()
	e.at[endedKey(session)] = max(session.LastActivityAt, time.Now().Add(endedGrace).UnixMilli())
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
	now := time.Now().UnixMilli()
	for _, session := range sessions {
		key := endedKey(session)
		at, ended := e.at[key]
		active := session.State == protocol.StateBusy || session.State == protocol.StateWaitingInput
		switch {
		case !ended, session.State == protocol.StateEnded:
			// An agent's own "ended" row is how it is picked up again.
			kept = append(kept, session)
		case (active && now >= at) || session.LastActivityAt > at:
			delete(e.at, key)
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
