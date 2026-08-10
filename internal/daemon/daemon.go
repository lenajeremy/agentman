// Package daemon runs on the user's machine and is the only component that
// touches agent data.
//
// It owns everything: discovering sessions, reading transcripts, receiving
// hooks, and answering the app's requests. The relay is a pipe, and the app is
// a view. That concentration is deliberate — it is what allows the relay to
// store nothing.
package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// discoverInterval is how often the session list is refreshed.
//
// Hooks report state changes immediately, so this only has to catch sessions
// appearing and disappearing — a poll is fine, and one second is well below
// what a human notices.
const discoverInterval = time.Second

// Transport is how the daemon reaches the app. Abstracted so the daemon can
// run over a relay websocket, a LAN listener, or an in-process pipe in tests.
type Transport interface {
	// Send delivers one event to every connected app.
	Send(event protocol.Event) error
}

// Daemon wires discovery, hooks, and subscriptions together.
type Daemon struct {
	registry *source.Registry
	sink     Transport

	mu sync.Mutex
	// sessions is the last discovered list, kept so hook events can be matched
	// against a known session without re-scanning the disk.
	sessions map[string]protocol.Session
	// follows holds the active live tail for each watched session. Only
	// sessions the app is actually watching appear here, which is what keeps
	// idle sessions free.
	follows map[string]*follow
}

// follow is one live tail. It is tracked by pointer identity so that a
// finishing tail can only ever cancel itself.
type follow struct {
	cancel context.CancelFunc
}

// New creates a daemon.
func New(registry *source.Registry, sink Transport) *Daemon {
	return &Daemon{
		registry: registry,
		sink:     sink,
		sessions: map[string]protocol.Session{},
		follows:  map[string]*follow{},
	}
}

// Run drives discovery and hook handling until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context, hooks <-chan hook.Event) error {
	ticker := time.NewTicker(discoverInterval)
	defer ticker.Stop()

	d.refresh(ctx, true)

	for {
		select {
		case <-ctx.Done():
			d.stopAllFollows()
			return nil

		case <-ticker.C:
			d.refresh(ctx, false)

		case event, ok := <-hooks:
			if !ok {
				hooks = nil // channel closed; keep discovery running
				continue
			}
			d.handleHook(event)
		}
	}
}

// refresh re-discovers sessions and reports what changed.
//
// Only differences are sent. A phone on cell data should not receive the full
// session list every second just to learn that nothing happened.
func (d *Daemon) refresh(ctx context.Context, initial bool) {
	found, err := d.registry.Discover(ctx)
	if err != nil {
		// Partial results are still worth reporting: one broken adapter must
		// not blank the user's whole list.
		_ = d.sink.Send(protocol.Event{Type: protocol.EvtError, Error: err.Error()})
	}

	d.mu.Lock()
	previous := d.sessions
	current := make(map[string]protocol.Session, len(found))
	for _, session := range found {
		current[session.ID] = session
	}
	d.sessions = current
	d.mu.Unlock()

	if initial {
		_ = d.sink.Send(protocol.Event{Type: protocol.EvtSessions, Sessions: found})
		return
	}

	for id, session := range current {
		before, existed := previous[id]
		if !existed || before != session {
			s := session
			_ = d.sink.Send(protocol.Event{Type: protocol.EvtSessionUpdate, Session: &s})
		}
	}
	for id := range previous {
		if _, still := current[id]; !still {
			d.stopFollow(id)
			_ = d.sink.Send(protocol.Event{Type: protocol.EvtSessionGone, SessionID: id})
		}
	}
}

// handleHook applies a hook event to session state and rings the phone when a
// turn completes.
func (d *Daemon) handleHook(event hook.Event) {
	state, hasState := event.State()

	d.mu.Lock()
	session, known := d.sessions[event.SessionID]
	if known && hasState && session.State != state {
		session.State = state
		session.LastActivityAt = event.ReceivedAt
		d.sessions[event.SessionID] = session
	}
	d.mu.Unlock()

	// A hook can arrive before discovery has seen the session — a brand new
	// session fires SessionStart immediately. Report state anyway; the next
	// sweep fills in the rest.
	if hasState && known {
		s := session
		_ = d.sink.Send(protocol.Event{Type: protocol.EvtSessionUpdate, Session: &s})
	}

	if event.IsTurnComplete() {
		name := session.Name
		if name == "" {
			name = "agent"
		}
		_ = d.sink.Send(protocol.Event{
			Type:        protocol.EvtTurnComplete,
			SessionID:   event.SessionID,
			SessionName: name,
			Preview:     event.Preview(),
		})
	}
}

// Handle answers one request from the app.
func (d *Daemon) Handle(ctx context.Context, req protocol.Request) protocol.Event {
	switch req.Type {
	case protocol.ReqListSessions:
		return protocol.Event{Type: protocol.EvtSessions, Sessions: d.snapshot()}

	case protocol.ReqFetchMessages:
		limit := req.Limit
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		page, err := d.registry.Page(ctx, req.SessionID, req.Before, limit)
		if err != nil {
			return protocol.Event{Type: protocol.EvtError, SessionID: req.SessionID, Error: err.Error()}
		}
		return protocol.Event{Type: protocol.EvtPage, Page: &page}

	case protocol.ReqSubscribe:
		d.startFollow(req.SessionID)
		return protocol.Event{}

	case protocol.ReqUnsubscribe:
		d.stopFollow(req.SessionID)
		return protocol.Event{}

	case protocol.ReqSendMessage:
		mode, err := d.registry.Inject(ctx, req.SessionID, req.Text)
		result := protocol.Event{
			Type:      protocol.EvtSendResult,
			SessionID: req.SessionID,
			ClientID:  req.ClientID,
			Status:    protocol.StatusDelivered,
		}
		switch {
		case err != nil:
			result.Status = protocol.StatusFailed
			result.Error = err.Error()
		case mode == protocol.InjectHook:
			// Not delivered — held until the current turn ends. The app must
			// show this distinctly rather than as a sent message.
			result.Status = protocol.StatusQueued
		}
		return result

	case protocol.ReqAnswer:
		if err := d.registry.Answer(ctx, req.SessionID, req.OptionKey); err != nil {
			return protocol.Event{
				Type: protocol.EvtSendResult, SessionID: req.SessionID,
				ClientID: req.ClientID, Status: protocol.StatusFailed, Error: err.Error(),
			}
		}
		// The next discovery sweep clears the question and updates the state,
		// so nothing else has to be reported here.
		return protocol.Event{
			Type: protocol.EvtSendResult, SessionID: req.SessionID,
			ClientID: req.ClientID, Status: protocol.StatusDelivered,
		}

	default:
		return protocol.Event{Type: protocol.EvtError, Error: "unsupported request: " + string(req.Type)}
	}
}

func (d *Daemon) snapshot() []protocol.Session {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Always non-nil: Go marshals a nil slice as JSON null, which a client
	// expecting an array will crash on.
	out := make([]protocol.Session, 0, len(d.sessions))
	for _, session := range d.sessions {
		out = append(out, session)
	}
	source.SortSessions(out)
	return out
}

// startFollow begins live-tailing a session, if it is not already followed.
func (d *Daemon) startFollow(sessionID string) {
	d.mu.Lock()
	if _, exists := d.follows[sessionID]; exists {
		d.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	handle := &follow{cancel: cancel}
	d.follows[sessionID] = handle
	d.mu.Unlock()

	messages := make(chan []protocol.Message, 8)
	go func() {
		// Retire only this tail. Cancelling by session id would be a race:
		// an app that resubscribes (React remounting a screen, or the user
		// navigating back and forth) produces subscribe → unsubscribe →
		// subscribe, and the first tail's cleanup would then cancel the
		// second one — leaving the app subscribed to a stream that had
		// already been shut down, receiving nothing.
		defer d.retireFollow(sessionID, handle)
		_ = d.registry.Follow(ctx, sessionID, messages)
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case batch := <-messages:
				_ = d.sink.Send(protocol.Event{
					Type:      protocol.EvtMessages,
					SessionID: sessionID,
					Messages:  batch,
				})
			}
		}
	}()
}

// stopFollow ends whatever tail is currently running for a session.
func (d *Daemon) stopFollow(sessionID string) {
	d.mu.Lock()
	handle, exists := d.follows[sessionID]
	delete(d.follows, sessionID)
	d.mu.Unlock()
	if exists {
		handle.cancel()
	}
}

// retireFollow removes a tail only if it is still the current one.
func (d *Daemon) retireFollow(sessionID string, handle *follow) {
	d.mu.Lock()
	if current, ok := d.follows[sessionID]; ok && current == handle {
		delete(d.follows, sessionID)
	}
	d.mu.Unlock()
	handle.cancel()
}

func (d *Daemon) stopAllFollows() {
	d.mu.Lock()
	handles := make([]*follow, 0, len(d.follows))
	for _, handle := range d.follows {
		handles = append(handles, handle)
	}
	d.follows = map[string]*follow{}
	d.mu.Unlock()
	for _, handle := range handles {
		handle.cancel()
	}
}

// DecodeRequest parses an app request from an envelope payload.
func DecodeRequest(payload json.RawMessage) (protocol.Request, error) {
	var req protocol.Request
	err := json.Unmarshal(payload, &req)
	return req, err
}
