package manager

import (
	"context"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// What the user has in front of them. An app tells Agentman which agents'
// threads are on screen (manager_viewing) and keeps telling it while they
// are. An agent the user is looking at gets no card and no push: its
// question is right there, and a card for it in Agentman's conversation, or
// a phone ringing, is noise. If it still waits on the user once they look
// away, its card goes up then.

// viewingFor is how long an app's word on what is on screen lasts unless it
// says it again: an app that crashes or sleeps holds nothing back for long.
const viewingFor = 2 * time.Minute

// Bounds on one app's word, which arrives over the wire.
const (
	maxViewing      = 16
	maxViewingID    = 256
	maxViewerClient = 128
)

type viewer struct {
	sessions map[string]bool
	until    time.Time
}

// setViewing records what one app has on screen, and puts up the cards held
// for agents no longer in front of the user.
func (m *Manager) setViewing(ctx context.Context, client string, sessions []string) protocol.Event {
	if len(client) > maxViewerClient {
		client = client[:maxViewerClient]
	}
	shown := map[string]bool{}
	for _, id := range sessions {
		if id != "" && len(id) <= maxViewingID && len(shown) < maxViewing {
			shown[id] = true
		}
	}
	m.viewMu.Lock()
	if m.viewers == nil {
		m.viewers = map[string]viewer{}
	}
	if len(shown) == 0 {
		delete(m.viewers, client)
	} else {
		m.viewers[client] = viewer{sessions: shown, until: m.now().Add(viewingFor)}
	}
	m.viewMu.Unlock()
	m.raiseHeldCards(ctx)
	return protocol.Event{Type: protocol.EvtManagerViewing}
}

// viewed reports whether some app has the agent in front of the user now.
func (m *Manager) viewed(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	now := m.now()
	m.viewMu.Lock()
	defer m.viewMu.Unlock()
	for client, v := range m.viewers {
		if now.After(v.until) {
			delete(m.viewers, client)
			continue
		}
		if v.sessions[sessionID] {
			return true
		}
	}
	return false
}

// holdQuestion remembers a question not put to the user as a card because
// they were looking at the agent asking it.
func (m *Manager) holdQuestion(sessionID, questionID string) {
	m.viewMu.Lock()
	defer m.viewMu.Unlock()
	if m.heldQuestions == nil {
		m.heldQuestions = map[string]string{}
	}
	m.heldQuestions[sessionID] = questionID
}

// holdNeed remembers that a brief said an agent needs the user while they
// were looking at it.
func (m *Manager) holdNeed(sessionID string) {
	m.viewMu.Lock()
	defer m.viewMu.Unlock()
	if m.heldNeeds == nil {
		m.heldNeeds = map[string]bool{}
	}
	m.heldNeeds[sessionID] = true
}

// raiseHeldCards puts up the cards held back for agents the user is no
// longer looking at, those that still wait on them. One answered while the
// user looked is simply forgotten.
func (m *Manager) raiseHeldCards(ctx context.Context) {
	m.viewMu.Lock()
	questions := make(map[string]string, len(m.heldQuestions))
	for id, q := range m.heldQuestions {
		questions[id] = q
	}
	needs := make([]string, 0, len(m.heldNeeds))
	for id := range m.heldNeeds {
		needs = append(needs, id)
	}
	m.viewMu.Unlock()
	if len(questions)+len(needs) == 0 {
		return
	}
	sessions := map[string]protocol.Session{}
	for _, s := range m.agents.Sessions() {
		sessions[s.ID] = s
	}
	for id, questionID := range questions {
		if m.viewed(id) {
			continue
		}
		m.forgetHeld(id)
		if s, ok := sessions[id]; ok && s.Question != nil && s.Question.ID == questionID {
			m.takeQuestion(ctx, s)
		}
	}
	for _, id := range needs {
		if m.viewed(id) {
			continue
		}
		m.forgetHeld(id)
		s, ok := sessions[id]
		brief, briefed := m.briefs.get(id)
		if ok && briefed && strings.TrimSpace(brief.NeedsYou) != "" && s.Question == nil {
			m.cardMu.Lock()
			m.postNeedsYouLocked(s, brief)
			m.cardMu.Unlock()
		}
	}
}

func (m *Manager) forgetHeld(sessionID string) {
	m.viewMu.Lock()
	defer m.viewMu.Unlock()
	delete(m.heldQuestions, sessionID)
	delete(m.heldNeeds, sessionID)
}

// watchViewing raises held cards whose app stopped saying what it shows:
// it quit, crashed or the Mac slept.
func (m *Manager) watchViewing(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.raiseHeldCards(ctx)
		}
	}
}
