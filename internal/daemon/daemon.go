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
	"unicode"
	"unicode/utf8"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/push"
	"github.com/lenajeremy/agentman/internal/source"
)

// discoverInterval is how often the session list is refreshed.
//
// Hooks report state changes immediately, so this only has to catch sessions
// appearing and disappearing — a poll is fine, and one second is well below
// what a human notices.
const discoverInterval = time.Second

// Session mutations are serialized before an adapter validates the current
// terminal state. This prevents two devices (or a double tap) from both
// validating question A and then typing the second answer into question B.
// A fixed stripe set avoids retaining one mutex for every session ever seen;
// rare hash collisions merely serialize unrelated actions briefly.
const actionLockStripes = 64

// turnQuestionGrace gives discovery time to distinguish "the turn finished"
// from "Claude stopped because it opened a question". Claude emits the same
// Stop hook for both, while the question exists only in the tmux pane and is
// normally found on the next one-second sweep.
const turnQuestionGrace = 2 * discoverInterval

// Hooks are authoritative but source files and APIs can lag behind them. Keep
// short-lived busy/idle states long enough for polling to catch up; a waiting
// state remains until newer activity or another hook resolves it because an
// unanswered permission prompt has no natural timeout.
const hookStateGrace = 5 * discoverInterval

// hookWaitingGrace bounds a "blocked on the user" lease.
//
// Claude fires its Notification hook for two different situations: the agent
// opened a permission prompt, and the prompt has simply sat idle for a while.
// Only the first is actionable, and nothing in the event distinguishes them.
// A lease that never expired pinned a session to "needs you" indefinitely on
// the strength of the second, with no question to answer.
//
// Generous, because a real permission prompt has no natural timeout, but
// finite, because being wrong here is worse than being late.
const hookWaitingGrace = 10 * time.Minute

const maxHookQuestionInspectionRetries = 3

// A discovery sweep can change hundreds of sessions at once (daemon restart,
// agent upgrade, large workspace). One full snapshot is both smaller and less
// bursty than enough individual updates to overflow an otherwise healthy
// websocket's bounded queue.
const maxIncrementalSessionEvents = 6

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
	// turns gives hook and polling completions one shared generation to claim.
	// Unlike a time window, it permits two real short turns to notify while
	// still making duplicate hook/poll reports for one turn idempotent.
	turns map[string]turnState
	// hookStates prevent the next stale discovery snapshot from immediately
	// undoing a state transition delivered by an agent hook.
	hookStates map[string]hookState
	// pendingTurns delays hook completion just long enough for discovery to
	// reveal a blocking question. Timers are keyed so duplicate hooks cannot
	// schedule duplicate alerts.
	pendingTurns       map[string]pendingTurn
	turnDelay          time.Duration
	questionRetryDelay time.Duration
	// follows holds the active live tail for each watched session. Only
	// sessions the app is actually watching appear here, which is what keeps
	// idle sessions free.
	follows map[string]*follow
	// actionLocks cover send, answer, and interrupt from validation through the
	// adapter action. Read-only history requests remain fully concurrent.
	actionLocks [actionLockStripes]sync.Mutex
	// push reaches a phone whose websocket is gone because iOS suspended the
	// app — the case a live-socket notification cannot cover. Nil until the CLI
	// supplies one, so tests and `-relay none` need no push configuration.
	push *push.Sender
	// pushedQuestions remembers the last question announced per session, so a
	// prompt sitting unanswered across many sweeps rings the phone once.
	pushedQuestions map[string]string
}

// follow is one live tail. It is tracked by pointer identity so that a
// finishing tail can only ever cancel itself.
type follow struct {
	cancel      context.CancelFunc
	subscribers map[string]struct{}
}

type pendingTurn struct {
	timer      *time.Timer
	token      *byte
	generation uint64
	attempt    int
}

type turnState struct {
	generation uint64
	notified   bool
}

type hookState struct {
	state protocol.State
	at    int64
	until time.Time
}

// New creates a daemon.
func New(registry *source.Registry, sink Transport) *Daemon {
	return &Daemon{
		registry:           registry,
		sink:               sink,
		sessions:           map[string]protocol.Session{},
		follows:            map[string]*follow{},
		turns:              map[string]turnState{},
		hookStates:         map[string]hookState{},
		pendingTurns:       map[string]pendingTurn{},
		turnDelay:          turnQuestionGrace,
		questionRetryDelay: discoverInterval,
		pushedQuestions:    map[string]string{},
	}
}

// SetPush installs the push sender. Separate from New so that a daemon without
// push configured is the default rather than a special case.
func (d *Daemon) SetPush(sender *push.Sender) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.push = sender
}

// alert delivers a notification to phones that are not currently listening.
//
// Fired in the background: the callers are a discovery sweep and a hook
// callback, and neither can afford to wait on a third-party HTTP request.
func (d *Daemon) alert(title, body, sessionID string) {
	d.mu.Lock()
	sender := d.push
	d.mu.Unlock()
	if sender == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = sender.Send(ctx, push.Alert{Title: title, Body: body, SessionID: sessionID})
	}()
}

// alertTurnComplete pushes a finished turn. The preview only travels when the
// user has opted in, since it leaves the machine.
func (d *Daemon) alertTurnComplete(event protocol.Event) {
	d.mu.Lock()
	sender := d.push
	d.mu.Unlock()
	if sender == nil {
		return
	}
	name := event.SessionName
	if name == "" {
		name = event.SessionID
	}
	body := sender.Preview(event.Preview)
	if body == "" {
		body = "Finished its turn."
	}
	d.alert(name, body, event.SessionID)
}

// alertNeedsAnswer pushes a session that has become blocked on a decision, once
// per distinct question.
func (d *Daemon) alertNeedsAnswer(session protocol.Session) {
	if session.Question == nil {
		return
	}
	d.mu.Lock()
	previous, seen := d.pushedQuestions[session.ID]
	if seen && previous == session.Question.ID {
		d.mu.Unlock()
		return
	}
	d.pushedQuestions[session.ID] = session.Question.ID
	sender := d.push
	d.mu.Unlock()
	if sender == nil {
		return
	}
	name := session.Name
	if name == "" {
		name = session.ID
	}
	body := sender.Preview(session.Question.Prompt)
	if body == "" {
		body = "Waiting on you to choose."
	}
	d.alert(name+" needs your answer", body, session.ID)
}

// Run drives discovery and hook handling until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context, hooks <-chan hook.Event) error {
	ticker := time.NewTicker(discoverInterval)
	defer ticker.Stop()
	defer d.stopPendingTurns()

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

	found = normalizeDiscoveredSessions(found)
	d.mu.Lock()
	previous := d.sessions
	current := make(map[string]protocol.Session, len(found))
	for _, session := range found {
		session = d.applyHookStateLocked(session, time.Now())
		current[session.ID] = session
		before, existed := previous[session.ID]
		if session.State == protocol.StateBusy && (!existed || before.State != protocol.StateBusy) {
			d.startTurnLocked(session.ID)
		}
	}
	// Publish a distinct map. Hook/timer callbacks mutate d.sessions under d.mu,
	// while the immutable local snapshot below is diffed and emitted after the
	// lock is released. Sharing the map here creates a real concurrent
	// iteration/write panic window.
	d.sessions = cloneSessionMap(current)
	d.mu.Unlock()

	if initial {
		_ = d.sink.Send(protocol.Event{Type: protocol.EvtSessions, Sessions: fitSessionList(found)})
		return
	}

	changed := make(map[string]struct{})
	for id, session := range current {
		before, existed := previous[id]
		if !existed || !before.SameAs(session) {
			changed[id] = struct{}{}
		}
	}
	var gone []string
	for id := range previous {
		if _, still := current[id]; !still {
			gone = append(gone, id)
		}
	}
	bulk := len(changed)+len(gone) > maxIncrementalSessionEvents
	if bulk {
		list := make([]protocol.Session, 0, len(current))
		for _, session := range current {
			list = append(list, session)
		}
		source.SortSessions(list)
		_ = d.sink.Send(protocol.Event{Type: protocol.EvtSessions, Sessions: fitSessionList(list)})
	}

	for id, session := range current {
		before, existed := previous[id]
		if _, didChange := changed[id]; didChange && !bulk {
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
		// A blocked agent is the most actionable thing the daemon knows, and the
		// one most likely to be missed: the phone is asleep precisely because the
		// user walked away. Announced once per distinct question.
		if session.Question != nil {
			d.alertNeedsAnswer(session)
		} else {
			d.mu.Lock()
			delete(d.pushedQuestions, id)
			d.mu.Unlock()
		}
	}
	for _, id := range gone {
		d.mu.Lock()
		delete(d.turns, id)
		delete(d.hookStates, id)
		delete(d.pushedQuestions, id)
		if pending, ok := d.pendingTurns[id]; ok {
			pending.timer.Stop()
			delete(d.pendingTurns, id)
		}
		d.mu.Unlock()
		d.stopFollowAll(id)
		if !bulk {
			_ = d.sink.Send(protocol.Event{Type: protocol.EvtSessionGone, SessionID: id})
		}
	}
}

func cloneSessionMap(sessions map[string]protocol.Session) map[string]protocol.Session {
	cloned := make(map[string]protocol.Session, len(sessions))
	for id, session := range sessions {
		cloned[id] = session
	}
	return cloned
}

// announceTurnComplete rings the phone for a turn that ended, unless a hook
// already did.
//
// The two paths overlap for any agent whose hooks work, and a notification
// arriving twice for one piece of work is worse than a slightly later one, so
// the hook wins and this fills the gap behind it.
func (d *Daemon) announceTurnComplete(ctx context.Context, session protocol.Session) {
	d.mu.Lock()
	generation := d.ensureTurnLocked(session.ID)
	claimed := d.claimTurnLocked(session.ID, generation)
	d.mu.Unlock()
	if !claimed {
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

	event := protocol.Event{
		Type:        protocol.EvtTurnComplete,
		SessionID:   session.ID,
		SessionName: session.Name,
		Preview:     preview,
	}
	_ = d.sink.Send(event)
	d.alertTurnComplete(event)
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
	event.SessionID = d.canonicalHookSessionIDLocked(event)
	session, known := d.sessions[event.SessionID]
	if hasState && state == protocol.StateBusy && (!known || session.State != protocol.StateBusy) {
		d.startTurnLocked(event.SessionID)
	}
	if known && hasState && session.State != state {
		receivedAt := event.ReceivedAt
		if receivedAt == 0 {
			receivedAt = time.Now().UnixMilli()
		}
		lease := hookState{state: state, at: receivedAt, until: time.Now().Add(hookStateGrace)}
		if state == protocol.StateWaitingInput {
			lease.until = time.Now().Add(hookWaitingGrace)
		}
		d.hookStates[event.SessionID] = lease
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
		d.scheduleHookTurn(protocol.Event{
			Type:        protocol.EvtTurnComplete,
			SessionID:   event.SessionID,
			SessionName: name,
			Preview:     event.Preview(),
		})
	}
}

// applyHookStateLocked reconciles a polled snapshot with a newer hook event.
// d.mu must be held by the caller.
func (d *Daemon) applyHookStateLocked(session protocol.Session, now time.Time) protocol.Session {
	latest, ok := d.hookStates[session.ID]
	if !ok {
		return session
	}
	// A parsed question is more specific than Claude's ambiguous Stop=idle
	// hook. Never hide it behind an older lifecycle state.
	if session.Question != nil || session.State == protocol.StateWaitingInput {
		if latest.state != protocol.StateWaitingInput {
			delete(d.hookStates, session.ID)
		}
		return session
	}
	// For a session we can see into, the pane settles what the hook could not.
	// Discovery parses any prompt actually on screen, so reaching here — no
	// question, on a tmux-backed session — means the notification was the idle
	// kind rather than a real block. Believing it would show "needs you" with
	// nothing to answer.
	if latest.state == protocol.StateWaitingInput && session.Inject == protocol.InjectTmux {
		delete(d.hookStates, session.ID)
		return session
	}
	if session.LastActivityAt > latest.at {
		delete(d.hookStates, session.ID)
		return session
	}
	if session.State == latest.state {
		if latest.state != protocol.StateWaitingInput {
			delete(d.hookStates, session.ID)
		}
		return session
	}
	if !latest.until.After(now) {
		delete(d.hookStates, session.ID)
		return session
	}
	session.State = latest.state
	session.LastActivityAt = max(session.LastActivityAt, latest.at)
	return session
}

// canonicalHookSessionIDLocked maps an agent-native hook id onto the stable
// app-visible session id. Codex tmux sessions are deliberately keyed by pane
// before their rollout exists, so the hook reports codex:<thread> while the
// phone knows codex:tmux-<pane>. NativeID is the bridge between the two.
// d.mu must be held by the caller.
func (d *Daemon) canonicalHookSessionIDLocked(event hook.Event) string {
	if _, known := d.sessions[event.SessionID]; known {
		return event.SessionID
	}
	_, nativeID, ok := strings.Cut(event.SessionID, ":")
	if !ok || nativeID == "" {
		return event.SessionID
	}
	for id, session := range d.sessions {
		if session.Kind == event.Kind && session.NativeID == nativeID {
			return id
		}
	}
	return event.SessionID
}

func (d *Daemon) scheduleHookTurn(event protocol.Event) {
	d.mu.Lock()
	delay := d.turnDelay
	generation := d.ensureTurnLocked(event.SessionID)
	// Codex exposes only a completion hook. When polling never catches its busy
	// state, each delivered notify callback is the only evidence of a new turn.
	// Claude has UserPromptSubmit and must instead keep duplicate Stop hooks on
	// the same generation.
	if state := d.turns[event.SessionID]; event.SessionID != "" &&
		strings.HasPrefix(event.SessionID, string(protocol.KindCodex)+":") && state.notified {
		d.startTurnLocked(event.SessionID)
		generation = d.turns[event.SessionID].generation
	}
	if previous, ok := d.pendingTurns[event.SessionID]; ok {
		previous.timer.Stop()
	}
	if delay <= 0 {
		delete(d.pendingTurns, event.SessionID)
		d.mu.Unlock()
		d.finishHookTurn(event, nil, generation, 0)
		return
	}
	token := new(byte)
	timer := time.AfterFunc(delay, func() { d.finishHookTurn(event, token, generation, 0) })
	d.pendingTurns[event.SessionID] = pendingTurn{
		timer: timer, token: token, generation: generation, attempt: 0,
	}
	d.mu.Unlock()
}

func (d *Daemon) finishHookTurn(
	event protocol.Event,
	token *byte,
	generation uint64,
	attempt int,
) {
	d.mu.Lock()
	if token != nil {
		pending, ok := d.pendingTurns[event.SessionID]
		if !ok || pending.token != token || pending.generation != generation ||
			pending.attempt != attempt {
			d.mu.Unlock()
			return
		}
	}
	delete(d.pendingTurns, event.SessionID)
	session, known := d.sessions[event.SessionID]
	if known && (session.State == protocol.StateWaitingInput || session.Question != nil) {
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()

	// Claude's Stop hook is ambiguous: it also fires when AskUserQuestion opens.
	// Whole-registry discovery can be delayed by an unrelated API adapter, so
	// inspect this one terminal pane directly before announcing completion.
	if strings.HasPrefix(event.SessionID, string(protocol.KindClaude)+":") {
		inspectCtx, cancel := context.WithTimeout(context.Background(), discoverInterval)
		question, inspectErr := d.registry.CurrentQuestion(inspectCtx, event.SessionID)
		cancel()
		if inspectErr != nil {
			if attempt < maxHookQuestionInspectionRetries {
				d.retryHookQuestionInspection(event, generation, attempt+1)
			}
			// Never translate "could not inspect" into "definitely finished".
			return
		}
		if question != nil {
			d.mu.Lock()
			current, stillKnown := d.sessions[event.SessionID]
			if stillKnown {
				current.Question = question
				current.State = protocol.StateWaitingInput
				d.sessions[event.SessionID] = current
			}
			d.mu.Unlock()
			if stillKnown {
				_ = d.sink.Send(protocol.Event{Type: protocol.EvtSessionUpdate, Session: &current})
			}
			return
		}
	}

	d.mu.Lock()
	// Discovery may have completed while the targeted pane check was running.
	// Reconcile that newer snapshot before claiming the completion generation.
	session, known = d.sessions[event.SessionID]
	if known && (session.State == protocol.StateWaitingInput || session.Question != nil) {
		d.mu.Unlock()
		return
	}
	// Claim the generation only for a real completion. A suppressed question must
	// not silence the completion notification for the next actual turn.
	claimed := d.claimTurnLocked(event.SessionID, generation)
	d.mu.Unlock()
	if !claimed {
		return
	}
	_ = d.sink.Send(event)
	d.alertTurnComplete(event)
}

func (d *Daemon) retryHookQuestionInspection(
	event protocol.Event,
	generation uint64,
	attempt int,
) {
	d.mu.Lock()
	state := d.turns[event.SessionID]
	if state.generation != generation || state.notified {
		d.mu.Unlock()
		return
	}
	if previous, ok := d.pendingTurns[event.SessionID]; ok {
		previous.timer.Stop()
	}
	token := new(byte)
	delay := d.questionRetryDelay
	if delay <= 0 {
		delay = discoverInterval
	}
	timer := time.AfterFunc(delay, func() {
		d.finishHookTurn(event, token, generation, attempt)
	})
	d.pendingTurns[event.SessionID] = pendingTurn{
		timer: timer, token: token, generation: generation, attempt: attempt,
	}
	d.mu.Unlock()
}

func (d *Daemon) startTurnLocked(sessionID string) uint64 {
	state := d.turns[sessionID]
	state.generation++
	state.notified = false
	d.turns[sessionID] = state
	return state.generation
}

func (d *Daemon) ensureTurnLocked(sessionID string) uint64 {
	state := d.turns[sessionID]
	if state.generation == 0 {
		return d.startTurnLocked(sessionID)
	}
	return state.generation
}

func (d *Daemon) claimTurnLocked(sessionID string, generation uint64) bool {
	state := d.turns[sessionID]
	if state.generation != generation || state.notified {
		return false
	}
	state.notified = true
	d.turns[sessionID] = state
	return true
}

func (d *Daemon) stopPendingTurns() {
	d.mu.Lock()
	for id, pending := range d.pendingTurns {
		pending.timer.Stop()
		delete(d.pendingTurns, id)
	}
	d.mu.Unlock()
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
	if requestMutatesSession(req.Type) {
		lock := d.actionLock(req.SessionID)
		lock.Lock()
		defer lock.Unlock()
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

	case protocol.ReqRegisterPush:
		d.mu.Lock()
		sender := d.push
		d.mu.Unlock()
		if sender == nil || sender.Store == nil {
			return protocol.Event{
				Type:  protocol.EvtError,
				Error: "daemon: push notifications are not configured on this machine",
			}
		}
		if _, err := sender.Store.Register(req.PushToken); err != nil {
			return protocol.Event{Type: protocol.EvtError, Error: err.Error()}
		}
		return protocol.Event{}

	case protocol.ReqSendMessage:
		d.mu.Lock()
		current, known := d.sessions[req.SessionID]
		d.mu.Unlock()
		if known && current.Question != nil {
			return protocol.Event{
				Type: protocol.EvtSendResult, SessionID: req.SessionID,
				ClientID: req.ClientID, Status: protocol.StatusFailed,
				Error: "daemon: answer the pending question before sending a message",
			}
		}
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
			QuestionID: req.QuestionID,
			OptionKey:  req.OptionKey,
			Options:    req.OptionKeys,
			Text:       req.AnswerText,
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

func requestMutatesSession(kind protocol.RequestType) bool {
	return kind == protocol.ReqSendMessage || kind == protocol.ReqAnswer ||
		kind == protocol.ReqInterrupt
}

func (d *Daemon) actionLock(sessionID string) *sync.Mutex {
	// FNV-1a, inlined to keep this hot path allocation-free.
	const (
		offset64 = uint64(14695981039346656037)
		prime64  = uint64(1099511628211)
	)
	hash := offset64
	for index := range len(sessionID) {
		hash ^= uint64(sessionID[index])
		hash *= prime64
	}
	return &d.actionLocks[hash%actionLockStripes]
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
	maxWireSessions     = 512
	maxWireOptions      = 64
	// These caps also account for JSON's worst-case six-byte escaping of one
	// control byte. A single normalized session_update, including 64 full
	// options, must remain below maxEventBytes—not only aggregate lists.
	maxWireNameBytes    = 2 << 10
	maxWirePathBytes    = 8 << 10
	maxWirePromptBytes  = 8 << 10
	maxWireDetailBytes  = 16 << 10
	maxWireOptionBytes  = 1 << 10
	maxWirePreviewBytes = 2 << 10
)

const wireTruncation = "\n\n[output truncated by agentman]"

func validateRequest(req protocol.Request) error {
	requiresSession := req.Type == protocol.ReqSubscribe || req.Type == protocol.ReqUnsubscribe ||
		req.Type == protocol.ReqFetchMessages || req.Type == protocol.ReqSendMessage ||
		req.Type == protocol.ReqInterrupt || req.Type == protocol.ReqAnswer
	if requiresSession && (req.SessionID == "" || len(req.SessionID) > maxSessionIDBytes) {
		return fmt.Errorf("daemon: invalid session id")
	}
	// The token is handed to a third-party API, so it is checked for shape here
	// rather than being forwarded on trust.
	if req.Type == protocol.ReqRegisterPush && !push.ValidToken(req.PushToken) {
		return fmt.Errorf("daemon: invalid push token")
	}
	if len(req.Before) > maxCursorBytes || len(req.ClientID) > maxClientIDBytes ||
		len(req.QuestionID) > maxClientIDBytes {
		return fmt.Errorf("daemon: request metadata is too large")
	}
	switch req.Type {
	case protocol.ReqListSessions, protocol.ReqSubscribe, protocol.ReqUnsubscribe,
		protocol.ReqFetchMessages, protocol.ReqInterrupt, protocol.ReqRegisterPush:
		return nil
	case protocol.ReqSendMessage:
		if strings.TrimSpace(req.Text) == "" {
			return fmt.Errorf("daemon: refusing to send an empty message")
		}
		if len(req.Text) > maxMessageBytes {
			return fmt.Errorf("daemon: message exceeds %d bytes", maxMessageBytes)
		}
		if containsTerminalControl(req.Text) {
			return fmt.Errorf("daemon: message contains terminal control characters")
		}
		return nil
	case protocol.ReqAnswer:
		if req.QuestionID == "" || len(req.OptionKeys) > 64 || len(req.AnswerText) > maxMessageBytes {
			return fmt.Errorf("daemon: invalid question answer")
		}
		total := len(req.OptionKey)
		for _, option := range req.OptionKeys {
			if option == "" || len(option) > maxOptionKeyBytes || containsTerminalControl(option) {
				return fmt.Errorf("daemon: invalid question answer")
			}
			total += len(option)
		}
		if len(req.OptionKey) > maxOptionKeyBytes || total > maxMessageBytes ||
			containsTerminalControl(req.OptionKey) || containsTerminalControl(req.AnswerText) ||
			(req.OptionKey == "" && len(req.OptionKeys) == 0 && strings.TrimSpace(req.AnswerText) == "") {
			return fmt.Errorf("daemon: invalid question answer")
		}
		return nil
	default:
		return fmt.Errorf("unsupported request: %s", req.Type)
	}
}

func containsTerminalControl(text string) bool {
	for _, character := range text {
		if character == '\n' || character == '\t' {
			continue
		}
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
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
	return fitSessionList(out)
}

func normalizeDiscoveredSessions(found []protocol.Session) []protocol.Session {
	if len(found) > maxWireSessions {
		found = found[:maxWireSessions]
	}
	normalized := make([]protocol.Session, 0, len(found))
	for _, session := range found {
		if session.ID == "" || len(session.ID) > maxSessionIDBytes ||
			len(session.NativeID) > maxSessionIDBytes {
			continue
		}
		session.Name = truncateWireText(session.Name, maxWireNameBytes)
		session.Cwd = truncateWireText(session.Cwd, maxWirePathBytes)
		session.Model = truncateWireText(session.Model, maxWireNameBytes)
		if question := session.Question; question != nil {
			copyQuestion := *question
			unsupportedReason := ""
			// Question IDs are round-trip concurrency tokens and cannot be
			// truncated without making every answer fail the adapter's stale
			// question check. A source that cannot produce a bounded token does
			// not get to expose an unsafe, unanswerable question on the wire.
			if copyQuestion.ID == "" || len(copyQuestion.ID) > maxClientIDBytes {
				session.Question = nil
				if session.State == protocol.StateWaitingInput {
					session.State = protocol.StateIdle
				}
				normalized = append(normalized, session)
				continue
			}
			copyQuestion.Title = truncateWireText(copyQuestion.Title, maxWireNameBytes)
			copyQuestion.Prompt = truncateWireText(copyQuestion.Prompt, maxWirePromptBytes)
			copyQuestion.Detail = truncateWireText(copyQuestion.Detail, maxWireDetailBytes)
			options := copyQuestion.Options
			if len(options) > maxWireOptions {
				options = nil
				unsupportedReason = "This question has too many options to answer safely from the app. Answer it in the terminal."
			}
			copyQuestion.Options = make([]protocol.QuestionOption, 0, len(options))
			for _, option := range options {
				// Keys are round-tripped to the adapter and cannot be truncated.
				if option.Key == "" || len(option.Key) > maxOptionKeyBytes ||
					containsTerminalControl(option.Key) {
					unsupportedReason = "This question contains an option that cannot be represented safely in the app. Answer it in the terminal."
					continue
				}
				option.Label = truncateWireText(option.Label, maxWireOptionBytes)
				option.Description = truncateWireText(option.Description, maxWireOptionBytes)
				option.Preview = truncateWireText(option.Preview, maxWirePreviewBytes)
				copyQuestion.Options = append(copyQuestion.Options, option)
			}
			if unsupportedReason != "" {
				// A partial decision is worse than no remote decision: hidden checked
				// options could be silently changed when the visible subset is sent.
				copyQuestion.Options = nil
				copyQuestion.Custom = false
				copyQuestion.Multiple = false
				copyQuestion.Detail = truncateWireText(
					strings.TrimSpace(copyQuestion.Detail)+"\n\n"+unsupportedReason,
					maxWireDetailBytes,
				)
			}
			if len(copyQuestion.Options) == 0 && !copyQuestion.Custom && unsupportedReason == "" {
				session.Question = nil
				if session.State == protocol.StateWaitingInput {
					session.State = protocol.StateIdle
				}
			} else {
				session.Question = &copyQuestion
			}
		}
		normalized = append(normalized, session)
	}
	return normalized
}

func fitSessionList(sessions []protocol.Session) []protocol.Session {
	fitted := make([]protocol.Session, 0, len(sessions))
	// Leave room for the surrounding event/envelope and JSON escaping variance.
	remaining := maxEventBytes - 4096
	for _, session := range sessions {
		encoded, err := json.Marshal(session)
		if err != nil || len(encoded)+1 > remaining {
			continue
		}
		fitted = append(fitted, session)
		remaining -= len(encoded) + 1
	}
	return fitted
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
