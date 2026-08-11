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
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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

// turnNotifyWindow is how long a hook-delivered notification suppresses the
// polled one for the same session. Comfortably longer than the gap between a
// Stop hook and the discovery sweep that sees the same transition.
const turnNotifyWindow = 15 * time.Second

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
	// notifiedAt records when a turn-complete was last announced for a session,
	// so the hook path and the polled path cannot both fire for the same turn.
	notifiedAt map[string]time.Time
	// follows holds the active live tail for each watched session. Only
	// sessions the app is actually watching appear here, which is what keeps
	// idle sessions free.
	follows map[string]*follow
}

// follow is one live tail. It is tracked by pointer identity so that a
// finishing tail can only ever cancel itself.
type follow struct {
	cancel      context.CancelFunc
	subscribers map[string]struct{}
}

// New creates a daemon.
func New(registry *source.Registry, sink Transport) *Daemon {
	return &Daemon{
		registry:   registry,
		sink:       sink,
		sessions:   map[string]protocol.Session{},
		follows:    map[string]*follow{},
		notifiedAt: map[string]time.Time{},
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
		if !existed || !before.SameAs(session) {
			s := session
			_ = d.sink.Send(protocol.Event{Type: protocol.EvtSessionUpdate, Session: &s})
		}
		// A turn that just ended. Hooks report this precisely, but only Claude
		// Code actually delivers them — Codex registers them and stays silent,
		// and OpenCode has no hook system at all. Without this the headline
		// feature, "your agent is done", simply never fires for two of the
		// three agents.
		if existed && before.State == protocol.StateBusy && session.State == protocol.StateIdle {
			d.announceTurnComplete(ctx, session)
		}
	}
	for id := range previous {
		if _, still := current[id]; !still {
			d.stopFollowAll(id)
			_ = d.sink.Send(protocol.Event{Type: protocol.EvtSessionGone, SessionID: id})
		}
	}
}

// announceTurnComplete rings the phone for a turn that ended, unless a hook
// already did.
//
// The two paths overlap for any agent whose hooks work, and a notification
// arriving twice for one piece of work is worse than a slightly later one, so
// the hook wins and this fills the gap behind it.
func (d *Daemon) announceTurnComplete(ctx context.Context, session protocol.Session) {
	d.mu.Lock()
	last, seen := d.notifiedAt[session.ID]
	recent := seen && time.Since(last) < turnNotifyWindow
	if !recent {
		d.notifiedAt[session.ID] = time.Now()
	}
	d.mu.Unlock()
	if recent {
		return
	}

	// A notification saying only "it finished" makes you open the app to learn
	// anything, which is the thing this is meant to save you.
	//
	// System messages count, not just the agent's own words: a turn that died
	// on a provider error produces no assistant text at all, and reporting that
	// as a bare "done" is actively misleading — it reads as success. Taking the
	// last of either kind gets this right without a special case, since the
	// failure is recorded after whatever content preceded it.
	preview := ""
	if page, err := d.registry.Page(ctx, session.ID, "", 6); err == nil {
		for i := len(page.Messages) - 1; i >= 0; i-- {
			m := page.Messages[i]
			if m.Text == "" {
				continue
			}
			if m.Role == protocol.RoleAssistant || m.Role == protocol.RoleSystem {
				preview = clipPreview(m.Text)
				break
			}
		}
	}

	_ = d.sink.Send(protocol.Event{
		Type:        protocol.EvtTurnComplete,
		SessionID:   session.ID,
		SessionName: session.Name,
		Preview:     preview,
	})
}

func clipPreview(text string) string {
	flat := strings.Join(strings.Fields(text), " ")
	runes := []rune(flat)
	if len(runes) <= 180 {
		return flat
	}
	return string(runes[:179]) + "…"
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
		// Claim the window so the polled path does not repeat this.
		d.mu.Lock()
		d.notifiedAt[event.SessionID] = time.Now()
		d.mu.Unlock()
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
	return d.HandleFrom(ctx, "local", req)
}

// HandleFrom answers one request and attributes subscription state to the app
// connection that sent it. Without that identity, one phone leaving a screen
// could unsubscribe a second phone that was still watching the same session.
func (d *Daemon) HandleFrom(
	ctx context.Context,
	subscriberID string,
	req protocol.Request,
) protocol.Event {
	if err := validateRequest(req); err != nil {
		if req.Type == protocol.ReqSendMessage || req.Type == protocol.ReqAnswer ||
			req.Type == protocol.ReqInterrupt {
			return protocol.Event{
				Type: protocol.EvtSendResult, SessionID: req.SessionID,
				ClientID: req.ClientID, Status: protocol.StatusFailed, Error: err.Error(),
			}
		}
		return protocol.Event{Type: protocol.EvtError, SessionID: req.SessionID, Error: err.Error()}
	}

	switch req.Type {
	case protocol.ReqListSessions:
		return protocol.Event{Type: protocol.EvtSessions, Sessions: d.snapshot()}

	case protocol.ReqFetchMessages:
		limit := req.Limit
		if limit <= 0 || limit > maxPageMessages {
			limit = maxPageMessages
		}
		page, err := d.registry.Page(ctx, req.SessionID, req.Before, limit)
		if err != nil {
			return protocol.Event{Type: protocol.EvtError, SessionID: req.SessionID, Error: err.Error()}
		}
		event := protocol.Event{Type: protocol.EvtPage, Page: &page}
		return fitMessageEvent(event)

	case protocol.ReqSubscribe:
		if err := d.startFollow(subscriberID, req.SessionID); err != nil {
			return protocol.Event{Type: protocol.EvtError, SessionID: req.SessionID, Error: err.Error()}
		}
		return protocol.Event{}

	case protocol.ReqUnsubscribe:
		d.stopFollow(subscriberID, req.SessionID)
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
		answer := protocol.QuestionAnswer{
			OptionKey: req.OptionKey,
			Options:   req.OptionKeys,
			Text:      req.AnswerText,
		}
		if err := d.registry.Answer(ctx, req.SessionID, answer); err != nil {
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

	case protocol.ReqInterrupt:
		if err := d.registry.Interrupt(ctx, req.SessionID); err != nil {
			return protocol.Event{
				Type: protocol.EvtSendResult, SessionID: req.SessionID,
				ClientID: req.ClientID, Status: protocol.StatusFailed, Error: err.Error(),
			}
		}
		return protocol.Event{
			Type: protocol.EvtSendResult, SessionID: req.SessionID,
			ClientID: req.ClientID, Status: protocol.StatusDelivered,
		}

	default:
		return protocol.Event{Type: protocol.EvtError, Error: "unsupported request: " + string(req.Type)}
	}
}

const (
	maxSessionIDBytes = 512
	maxCursorBytes    = 4096
	maxMessageBytes   = 64 * 1024
	maxOptionKeyBytes = 4096
	maxClientIDBytes  = 256
	// The relay accepts a 4 MiB websocket frame. Keep normalized events below
	// that ceiling with room for the envelope and JSON escaping. Without this,
	// one unusually long assistant response makes the relay close the daemon
	// connection and the phone appears to go mysteriously offline.
	maxEventBytes       = 3 << 20
	maxPageMessages     = 40
	maxWireMessageBytes = 256 << 10
	maxWireToolBytes    = 32 << 10
)

const wireTruncation = "\n\n[output truncated by agentman]"

func validateRequest(req protocol.Request) error {
	requiresSession := req.Type == protocol.ReqSubscribe || req.Type == protocol.ReqUnsubscribe ||
		req.Type == protocol.ReqFetchMessages || req.Type == protocol.ReqSendMessage ||
		req.Type == protocol.ReqInterrupt || req.Type == protocol.ReqAnswer
	if requiresSession && (req.SessionID == "" || len(req.SessionID) > maxSessionIDBytes) {
		return fmt.Errorf("daemon: invalid session id")
	}
	if len(req.Before) > maxCursorBytes || len(req.ClientID) > maxClientIDBytes {
		return fmt.Errorf("daemon: request metadata is too large")
	}
	switch req.Type {
	case protocol.ReqListSessions, protocol.ReqSubscribe, protocol.ReqUnsubscribe,
		protocol.ReqFetchMessages, protocol.ReqInterrupt:
		return nil
	case protocol.ReqSendMessage:
		if strings.TrimSpace(req.Text) == "" {
			return fmt.Errorf("daemon: refusing to send an empty message")
		}
		if len(req.Text) > maxMessageBytes {
			return fmt.Errorf("daemon: message exceeds %d bytes", maxMessageBytes)
		}
		return nil
	case protocol.ReqAnswer:
		if len(req.OptionKeys) > 64 || len(req.AnswerText) > maxMessageBytes {
			return fmt.Errorf("daemon: invalid question answer")
		}
		total := len(req.OptionKey)
		for _, option := range req.OptionKeys {
			if option == "" || len(option) > maxOptionKeyBytes {
				return fmt.Errorf("daemon: invalid question answer")
			}
			total += len(option)
		}
		if len(req.OptionKey) > maxOptionKeyBytes || total > maxMessageBytes ||
			(req.OptionKey == "" && len(req.OptionKeys) == 0 && strings.TrimSpace(req.AnswerText) == "") {
			return fmt.Errorf("daemon: invalid question answer")
		}
		return nil
	default:
		return fmt.Errorf("unsupported request: %s", req.Type)
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
func (d *Daemon) startFollow(subscriberID, sessionID string) error {
	if subscriberID == "" {
		subscriberID = "local"
	}
	d.mu.Lock()
	if _, exists := d.sessions[sessionID]; !exists {
		d.mu.Unlock()
		return fmt.Errorf("daemon: cannot subscribe to unknown session %q", sessionID)
	}
	if existing, exists := d.follows[sessionID]; exists {
		existing.subscribers[subscriberID] = struct{}{}
		d.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	handle := &follow{
		cancel: cancel, subscribers: map[string]struct{}{subscriberID: {}},
	}
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
		if err := d.registry.Follow(ctx, sessionID, messages); err != nil && ctx.Err() == nil {
			_ = d.sink.Send(protocol.Event{
				Type: protocol.EvtError, SessionID: sessionID, Error: err.Error(),
			})
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case batch := <-messages:
				for len(batch) > 0 {
					take := min(len(batch), maxPageMessages)
					event := protocol.Event{
						Type:      protocol.EvtMessages,
						SessionID: sessionID,
						Messages:  batch[:take],
					}
					_ = d.sink.Send(fitMessageEvent(event))
					batch = batch[take:]
				}
			}
		}
	}()
	return nil
}

// stopFollow ends whatever tail is currently running for a session.
func (d *Daemon) stopFollow(subscriberID, sessionID string) {
	if subscriberID == "" {
		subscriberID = "local"
	}
	d.mu.Lock()
	handle, exists := d.follows[sessionID]
	if exists {
		delete(handle.subscribers, subscriberID)
		if len(handle.subscribers) == 0 {
			delete(d.follows, sessionID)
		} else {
			exists = false
		}
	}
	d.mu.Unlock()
	if exists {
		handle.cancel()
	}
}

func (d *Daemon) stopFollowAll(sessionID string) {
	d.mu.Lock()
	handle, exists := d.follows[sessionID]
	delete(d.follows, sessionID)
	d.mu.Unlock()
	if exists {
		handle.cancel()
	}
}

// DisconnectSubscriber releases every follow owned by one app connection.
func (d *Daemon) DisconnectSubscriber(subscriberID string) {
	if subscriberID == "" {
		return
	}
	d.mu.Lock()
	var cancel []context.CancelFunc
	for sessionID, handle := range d.follows {
		delete(handle.subscribers, subscriberID)
		if len(handle.subscribers) == 0 {
			delete(d.follows, sessionID)
			cancel = append(cancel, handle.cancel)
		}
	}
	d.mu.Unlock()
	for _, stop := range cancel {
		stop()
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

// fitMessageEvent makes a private copy of message data and bounds it for the
// websocket protocol. It never drops a message: history cursors would then
// skip content the app had no way to request again. Instead, exceptionally
// large bodies are visibly truncated, while live batches are split by the
// caller before reaching this function.
func fitMessageEvent(event protocol.Event) protocol.Event {
	var messages []protocol.Message
	switch {
	case event.Page != nil:
		page := *event.Page
		page.Messages = cloneMessages(page.Messages)
		event.Page = &page
		messages = page.Messages
	case event.Messages != nil:
		event.Messages = cloneMessages(event.Messages)
		messages = event.Messages
	default:
		return event
	}

	for i := range messages {
		messages[i].Text = truncateWireText(messages[i].Text, maxWireMessageBytes)
		if messages[i].Tool != nil {
			tool := *messages[i].Tool
			tool.Name = truncateUTF8(tool.Name, maxOptionKeyBytes, "")
			tool.Summary = truncateUTF8(tool.Summary, maxWireToolBytes, wireTruncation)
			messages[i].Tool = &tool
		}
	}

	// JSON escaping can expand control-heavy strings, so raw byte caps alone
	// are not sufficient. Measure the actual event and progressively shrink the
	// largest body until it is safely below the on-wire ceiling.
	for {
		encoded, err := json.Marshal(event)
		if err != nil || len(encoded) <= maxEventBytes {
			return event
		}
		largest := -1
		for i := range messages {
			if largest < 0 || len(messages[i].Text) > len(messages[largest].Text) {
				largest = i
			}
		}
		if largest < 0 || messages[largest].Text == "" {
			return event
		}
		over := len(encoded) - maxEventBytes + 1024
		limit := len(messages[largest].Text) - over
		if limit < 0 {
			limit = 0
		}
		messages[largest].Text = truncateWireText(messages[largest].Text, limit)
	}
}

func cloneMessages(messages []protocol.Message) []protocol.Message {
	out := append([]protocol.Message(nil), messages...)
	return out
}

func truncateWireText(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	text = strings.TrimSuffix(text, wireTruncation)
	return truncateUTF8(text, maxBytes, wireTruncation)
}

func truncateUTF8(text string, maxBytes int, suffix string) string {
	if len(text) <= maxBytes {
		return text
	}
	if maxBytes <= 0 {
		return ""
	}
	if len(suffix) >= maxBytes {
		suffix = ""
	}
	cut := maxBytes - len(suffix)
	for cut > 0 && !utf8.ValidString(text[:cut]) {
		cut--
	}
	return text[:cut] + suffix
}

// DecodeRequest parses an app request from an envelope payload.
func DecodeRequest(payload json.RawMessage) (protocol.Request, error) {
	var req protocol.Request
	err := json.Unmarshal(payload, &req)
	return req, err
}
