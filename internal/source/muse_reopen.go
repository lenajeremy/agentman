package source

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Reopening an old Muse session from the apps. Nothing needs to start: any
// session Muse keeps can take a message (Agentman takes it for the turn), so
// reopening only lists it again, as a live one the apps can send to, until
// it has been quiet for the live window. One a terminal has open stays the
// terminal's.

type museReopens struct {
	mu sync.Mutex
	at map[string]time.Time // session id → when it was reopened
}

// errMuseOpenElsewhere is a reopen refused because a terminal has the
// session; the apps show it as it reads.
var errMuseOpenElsewhere = errors.New("Muse has this session open in a terminal. Close it there to send from here.")

// Reopen implements the daemon's reopen-in-place: the session is listed
// again, ready for a message.
func (s *MuseSource) Reopen(ctx context.Context, sessionID string) error {
	native := strings.TrimPrefix(sessionID, musePrefix)
	if s.openInTerminal()[native] && !s.holding(native) {
		return errMuseOpenElsewhere
	}
	var read museRead
	if err := s.host.call(ctx, "session/read", map[string]any{"sessionId": native, "excludeItems": true}, &read); err != nil {
		return err
	}
	s.reopens.mu.Lock()
	defer s.reopens.mu.Unlock()
	if s.reopens.at == nil {
		s.reopens.at = map[string]time.Time{}
	}
	s.reopens.at[native] = time.Now()
	return nil
}

// reopened is the sessions reopened within the live window; older ones are
// forgotten, and listed again only if they were active since.
func (s *MuseSource) reopened() []string {
	s.reopens.mu.Lock()
	defer s.reopens.mu.Unlock()
	ids := make([]string, 0, len(s.reopens.at))
	for id, at := range s.reopens.at {
		if time.Since(at) > museLiveWindow {
			delete(s.reopens.at, id)
			continue
		}
		ids = append(ids, id)
	}
	return ids
}
