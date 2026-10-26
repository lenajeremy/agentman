package daemon

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/push"
)

// Who notifies. While Agentman is on, it hears every agent and tells the
// user what matters, so it is the one voice: an agent's own alerts reach only
// the devices that asked for every agent, or for that agent. Off, the agents
// speak for themselves as they always have.
//
// The agents the user asked to hear from directly are kept in alerts.json,
// by session ID, so the phone, the Mac and push all see the same choice.

const maxAgentAlerts = 500

// agentAlerts is the agents the user asked to hear from directly, with when.
type agentAlerts struct {
	mu     sync.Mutex
	path   string
	agents map[string]int64
}

func loadAgentAlerts(path string) *agentAlerts {
	a := &agentAlerts{path: path, agents: map[string]int64{}}
	if path == "" {
		return a
	}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &a.agents)
	}
	return a
}

func (a *agentAlerts) has(id string) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.agents[id]
	return ok
}

func (a *agentAlerts) set(id string, on bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if on {
		a.agents[id] = time.Now().UnixMilli()
	} else {
		delete(a.agents, id)
	}
	// Sessions come and go; the oldest choices are let go first.
	if len(a.agents) > maxAgentAlerts {
		ids := make([]string, 0, len(a.agents))
		for id := range a.agents {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return a.agents[ids[i]] < a.agents[ids[j]] })
		for _, old := range ids[:len(ids)-maxAgentAlerts] {
			delete(a.agents, old)
		}
	}
	if a.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(a.agents, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.path)
}

// SetAlertsFile keeps the agents the user asked to hear from in path.
func (d *Daemon) SetAlertsFile(path string) {
	alerts := loadAgentAlerts(path)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.agentAlerts = alerts
}

// agentmanOn reports whether Agentman is on, and so speaks for the agents.
func (d *Daemon) agentmanOn() bool {
	manager := d.managerHook()
	return manager != nil && manager.On()
}

// covered reports whether an agent's own alert is Agentman's to give: it is
// on, and the user hasn't asked to hear from this agent itself.
func (d *Daemon) covered(sessionID string) bool {
	if !d.agentmanOn() {
		return false
	}
	d.mu.Lock()
	alerts := d.agentAlerts
	d.mu.Unlock()
	return !alerts.has(sessionID)
}

// setAgentAlerts turns an agent's own alerts on or off and tells every app.
func (d *Daemon) setAgentAlerts(req protocol.Request) protocol.Event {
	d.mu.Lock()
	session, known := d.sessions[req.SessionID]
	if d.agentAlerts == nil {
		d.agentAlerts = loadAgentAlerts("")
	}
	alerts := d.agentAlerts
	d.mu.Unlock()
	if !known {
		return protocol.Event{Type: protocol.EvtError, SessionID: req.SessionID, Error: "no such session"}
	}
	on := *req.Alerts
	if err := alerts.set(req.SessionID, on); err != nil {
		return protocol.Event{Type: protocol.EvtError, SessionID: req.SessionID, Error: "couldn't save that: " + err.Error()}
	}
	d.mu.Lock()
	if current, ok := d.sessions[req.SessionID]; ok {
		current.Alerts = on
		d.sessions[req.SessionID] = current
		session = current
	}
	d.mu.Unlock()
	update := session
	_ = d.sink.Send(protocol.Event{Type: protocol.EvtSessionUpdate, Session: &update})
	return protocol.Event{Type: protocol.EvtSessionUpdate, Session: &update}
}

// AlertAgentman pushes what Agentman tells the user on its own. A card that
// needs them rings like an agent needing them; anything else is Agentman's
// own kind, at most once a coalescing window, so a run of briefs is one
// alert. The tap opens Agentman, where all of it is.
func (d *Daemon) AlertAgentman(entry protocol.ManagerEntry) {
	d.mu.Lock()
	sender := d.push
	session := d.sessions[entry.SessionID]
	d.mu.Unlock()
	if sender == nil {
		return
	}
	name := session.Name
	if name == "" && entry.Card != nil {
		name = entry.Card.AgentName
	}
	if name == "" {
		name = "An agent"
	}

	alert := push.Alert{Kind: push.KindAgentman, Title: "Agentman", SessionID: entry.SessionID, Agentman: true}
	if card := entry.Card; card != nil && card.Kind != protocol.CardNote {
		alert.Kind = push.KindNeedsYou
		alert.Title = name + " needs you"
		alert.Body = sender.Preview(strings.TrimSpace(card.Title + "\n" + card.Detail))
		if alert.Body == "" {
			alert.Body = "Agentman has a card for you."
		}
		d.alert(alert)
		return
	}

	now := time.Now()
	d.mu.Lock()
	if now.Sub(d.lastAgentmanAlert) < d.alertCoalesce {
		d.mu.Unlock()
		return
	}
	d.lastAgentmanAlert = now
	d.mu.Unlock()
	alert.Body = sender.Preview(entry.Text)
	if alert.Body == "" {
		switch {
		case entry.Card != nil:
			alert.Body = "Agentman suggests a note."
		case entry.Role == "brief":
			alert.Body = "News about " + name + "."
		case entry.Role == "agent":
			alert.Body = name + " told Agentman something for you."
		default:
			alert.Body = "Agentman replied."
		}
	}
	d.alert(alert)
}
