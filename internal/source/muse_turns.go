package source

import (
	"encoding/json"
	"sync"
)

// Muse holds a turn open after its answer while background reminder checks
// run (each a model call; 8–16 s in practice), and says so with
// turn/foregroundCompleted. To the person the agent is done then: its answer
// is there and a message sent now is queued. So a session whose foreground
// finished reads as idle until its next turn starts, or until it does more
// work because a reminder fired.

type museForeground struct {
	mu   sync.Mutex
	done map[string]string // session id → turn whose foreground completed
}

// trackForeground keeps museForeground in step with the connection's events.
func (s *MuseSource) trackForeground(message museMessage) {
	var params struct {
		SessionID string `json:"sessionId"`
		TurnID    string `json:"turnId"`
		Item      struct {
			Kind string `json:"kind"`
		} `json:"item"`
	}
	switch message.Method {
	case "turn/foregroundCompleted", "turn/started", "turn/completed", "turn/unqueued", "item/started":
	default:
		return
	}
	if json.Unmarshal(message.Params, &params) != nil || params.SessionID == "" {
		return
	}
	s.foreground.mu.Lock()
	defer s.foreground.mu.Unlock()
	switch message.Method {
	case "turn/foregroundCompleted":
		if s.foreground.done == nil {
			s.foreground.done = map[string]string{}
		}
		s.foreground.done[params.SessionID] = params.TurnID
	case "item/started":
		// A reminder fired and the agent works again.
		switch params.Item.Kind {
		case "agentMessage", "toolCall", "userShell", "webSearch":
			delete(s.foreground.done, params.SessionID)
		}
	default:
		delete(s.foreground.done, params.SessionID)
	}
}

// foregroundDone reports a session whose answer is in while Muse finishes
// its background checks.
func (s *MuseSource) foregroundDone(native string) bool {
	s.foreground.mu.Lock()
	defer s.foreground.mu.Unlock()
	_, done := s.foreground.done[native]
	return done
}
