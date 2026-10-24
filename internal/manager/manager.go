// Package manager is Agentman, the agent manager: one conversation, in the
// daemon, that reads every agent for the user and briefs them on what
// happened.
//
// It runs on the Claude API with the user's own key, kept in the Keychain.
// Apps talk to it with the manager_* requests and hear it through the
// manager_* events (internal/protocol/manager.go). The daemon tells it when
// an agent finishes a turn or needs the user, and it reads agents through the
// daemon's own request handler, exactly as an app would.
package manager

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/knowledge"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/secrets"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// Config is what a Manager needs from am serve.
type Config struct {
	// Dir is where the conversation, briefs and settings are kept,
	// ~/.agentman/manager.
	Dir     string
	Secrets secrets.Store
	Agents  Agents
	// Knowledge is the user's notes, ~/.agentman/knowledge. Without it, the
	// notes live beside the conversation.
	Knowledge *knowledge.Store
	// Emit sends an event to every connected app.
	Emit func(protocol.Event)
	// NewModel makes a model client from an API key. Tests replace it.
	NewModel func(apiKey string) Model
	// ProcessTree reads the process table, to tell which agent is asking.
	// Tests replace it.
	ProcessTree func(context.Context) (*tmux.ProcessTree, error)
	// Now is the clock schedules run on. Tests replace it.
	Now func() time.Time
}

type Manager struct {
	dir          string
	secrets      secrets.Store
	agents       Agents
	emitEvent    func(protocol.Event)
	newModel     func(string) Model
	processTree  func(context.Context) (*tmux.ProcessTree, error)
	conversation *conversation
	briefs       *briefStore
	knowledge    *knowledge.Store
	keys         *keyStore
	schedules    *scheduleStore
	now          func() time.Time

	mu       sync.Mutex
	settings protocol.ManagerSettings
	client   Model
	keySet   bool
	status   protocol.ManagerStatus
	// stopTurn cancels the reply being written, if there is one.
	stopTurn context.CancelFunc

	// notes are what happened outside a reply, for the model to read with
	// the user's next message. Guarded by mu.
	notes []string
	// wakeups are turns Agentman's own schedules started, waiting for the
	// reply being written. Guarded by mu.
	wakeups []wakeup

	briefMu     sync.Mutex
	briefDue    map[string]time.Time
	lastBriefAt map[string]time.Time
	// asked is the question each agent was last seen asking, so a question
	// is taken once, not on every sweep that sees it.
	asked map[string]string

	// cardMu orders every change to a card: posting, answering, settling.
	cardMu sync.Mutex
	// lastState is each agent's state at the last check, to see one pick up.
	lastState map[string]protocol.State

	sessionsMu      sync.Mutex
	latestSessions  []protocol.Session
	sessionsChanged chan struct{}
}

// New loads the manager's state from cfg.Dir.
func New(cfg Config) (*Manager, error) {
	if cfg.Agents == nil || cfg.Secrets == nil {
		return nil, errors.New("manager: needs agents and a secret store")
	}
	conversation, err := loadConversation(cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("manager: load the conversation: %w", err)
	}
	m := &Manager{
		dir:          cfg.Dir,
		secrets:      cfg.Secrets,
		agents:       cfg.Agents,
		emitEvent:    cfg.Emit,
		newModel:     cfg.NewModel,
		processTree:  cfg.ProcessTree,
		conversation: conversation,
		briefs:       loadBriefs(cfg.Dir),
		knowledge:    cfg.Knowledge,
		keys:         loadKeys(cfg.Dir),
		schedules:    loadSchedules(cfg.Dir),
		now:          cfg.Now,
		settings:     loadSettings(filepath.Join(cfg.Dir, "settings.json")),
		briefDue:     map[string]time.Time{},
		lastBriefAt:  map[string]time.Time{},
		asked:        map[string]string{},
		lastState:    map[string]protocol.State{},

		sessionsChanged: make(chan struct{}, 1),
	}
	if m.newModel == nil {
		m.newModel = newClaudeModel
	}
	if m.processTree == nil {
		m.processTree = tmux.SnapshotProcessTree
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.knowledge == nil {
		m.knowledge = knowledge.Open(filepath.Join(cfg.Dir, "knowledge"))
	}
	if m.emitEvent == nil {
		m.emitEvent = func(protocol.Event) {}
	}
	_, keyErr := m.apiKey(context.Background())
	m.keySet = keyErr == nil
	m.status = protocol.ManagerStatus{State: "idle", KeySet: m.keySet}
	if !m.keySet {
		m.status.State = "needs_key"
	}
	return m, nil
}

// Run writes briefs, keeps the cards current and fires what Agentman
// scheduled until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	go m.watchSessions(ctx)
	go m.runSchedules(ctx)
	m.runBriefs(ctx)
}

// Handles reports the requests that are the manager's.
func (m *Manager) Handles(kind protocol.RequestType) bool {
	switch kind {
	case protocol.ReqManagerSend, protocol.ReqManagerHistory, protocol.ReqManagerStop,
		protocol.ReqManagerSettings, protocol.ReqManagerSetSettings, protocol.ReqManagerSetKey,
		protocol.ReqManagerBriefs, protocol.ReqManagerCardReply, protocol.ReqManagerCardDismiss,
		protocol.ReqKnowledgeList, protocol.ReqKnowledgeRead, protocol.ReqKnowledgeWrite, protocol.ReqKnowledgeDelete,
		protocol.ReqKeysList, protocol.ReqKeySet, protocol.ReqKeyDelete,
		protocol.ReqAgentKnowledgeSearch, protocol.ReqAgentKnowledgeRead, protocol.ReqAgentKeys,
		protocol.ReqAgentRequestKey, protocol.ReqAgentTell,
		protocol.ReqManagerSchedules, protocol.ReqManagerScheduleCancel, protocol.ReqManagerImage:
		return true
	}
	return false
}

// Handle answers one manager request.
func (m *Manager) Handle(ctx context.Context, req protocol.Request) protocol.Event {
	switch req.Type {
	case protocol.ReqManagerSend:
		return m.send(ctx, req.Text, req.Images...)
	case protocol.ReqManagerImage:
		return m.readImage(req.Path)
	case protocol.ReqManagerHistory:
		limit := req.Limit
		if limit <= 0 || limit > 200 {
			limit = 60
		}
		entries, more := m.conversation.page(req.Before, limit)
		return protocol.Event{Type: protocol.EvtManagerHistory, Entries: entries, More: more}
	case protocol.ReqManagerStop:
		m.mu.Lock()
		if m.stopTurn != nil {
			m.stopTurn()
		}
		m.mu.Unlock()
		return m.statusEvent()
	case protocol.ReqManagerSettings:
		return m.settingsEvent()
	case protocol.ReqManagerSetSettings:
		return m.setSettings(req.Manager)
	case protocol.ReqManagerSetKey:
		return m.setKey(ctx, req.Text)
	case protocol.ReqManagerBriefs:
		return protocol.Event{Type: protocol.EvtManagerBrief, Briefs: m.briefs.all()}
	case protocol.ReqManagerCardReply:
		return m.replyToCard(ctx, req)
	case protocol.ReqManagerCardDismiss:
		return m.dismissCard(req.CardID)
	case protocol.ReqKnowledgeList, protocol.ReqKnowledgeRead, protocol.ReqKnowledgeWrite, protocol.ReqKnowledgeDelete:
		return m.handleKnowledge(req)
	case protocol.ReqKeysList:
		return protocol.Event{Type: protocol.EvtKeys, Keys: m.keys.list()}
	case protocol.ReqKeySet:
		return m.saveKey(ctx, req.Key, req.Text)
	case protocol.ReqKeyDelete:
		return m.deleteKey(ctx, req.Path)
	case protocol.ReqAgentKnowledgeSearch, protocol.ReqAgentKnowledgeRead, protocol.ReqAgentKeys,
		protocol.ReqAgentRequestKey, protocol.ReqAgentTell:
		return m.handleAgent(ctx, req)
	case protocol.ReqManagerSchedules:
		return protocol.Event{Type: protocol.EvtManagerSchedules, Schedules: m.schedules.list()}
	case protocol.ReqManagerScheduleCancel:
		item, err := m.cancelSchedule(req.ScheduleID)
		if err != nil {
			return protocol.Event{Type: protocol.EvtError, Error: err.Error()}
		}
		m.note(fmt.Sprintf("The user cancelled your %s [%s]: %s", item.Kind, item.ID, scheduleLabel(item)))
		return protocol.Event{Type: protocol.EvtManagerSchedules, Schedules: m.schedules.list()}
	}
	return protocol.Event{Type: protocol.EvtError, Error: "unknown manager request"}
}

// TurnComplete hears that an agent finished a turn.
func (m *Manager) TurnComplete(session protocol.Session) {
	m.scheduleBrief(session.ID, briefAfterTurn, briefTurnGap)
	go m.watchEvent(session.ID, protocol.WatchTurnEnd, protocol.WatchIdle)
}

// NeedsAnswer hears that an agent is asking something. The daemon says so on
// every sweep that sees the question; it is taken once: approved, or put in
// front of the user as a card, at once and without a call to the model.
func (m *Manager) NeedsAnswer(session protocol.Session) {
	if session.Question == nil {
		return
	}
	go m.watchEvent(session.ID, protocol.WatchNeedsUser)
	m.briefMu.Lock()
	seen := m.asked[session.ID] == session.Question.ID
	m.asked[session.ID] = session.Question.ID
	m.briefMu.Unlock()
	if !seen {
		go m.takeQuestion(context.Background(), session)
	}
}

func (m *Manager) send(ctx context.Context, text string, images ...protocol.InlineImage) protocol.Event {
	text = strings.TrimSpace(text)
	if text == "" && len(images) == 0 {
		return protocol.Event{Type: protocol.EvtError, Error: "Say something to Agentman."}
	}
	model, err := m.model(ctx)
	if err != nil {
		m.setStatus("needs_key", "")
		return protocol.Event{Type: protocol.EvtError, Error: "Add your Anthropic API key in Settings first."}
	}
	m.mu.Lock()
	if m.stopTurn != nil {
		m.mu.Unlock()
		return protocol.Event{Type: protocol.EvtError, Error: "Agentman is still replying. Stop it first, or wait."}
	}
	// Not the request's context: the reply carries on if the app that asked
	// disconnects, and every app hears it.
	turnCtx, cancel := context.WithCancel(context.Background())
	m.stopTurn = cancel
	m.mu.Unlock()

	entryID := newID()
	names, blocks, err := m.keepImages(entryID, images)
	if err != nil {
		m.mu.Lock()
		m.stopTurn = nil
		m.mu.Unlock()
		cancel()
		return protocol.Event{Type: protocol.EvtError, Error: "Couldn't send that: " + err.Error()}
	}
	entry := m.addEntry(protocol.ManagerEntry{ID: entryID, Role: "user", Text: text, Images: names})
	go m.runTurn(turnCtx, model, turnInput{text: text, images: blocks})
	return protocol.Event{Type: protocol.EvtManagerEntry, Entry: &entry}
}

// addEntry gives an entry its ID and time, keeps it, and tells every app.
func (m *Manager) addEntry(entry protocol.ManagerEntry) protocol.ManagerEntry {
	if entry.ID == "" {
		entry.ID = newID()
	}
	if entry.Ts == 0 {
		entry.Ts = time.Now().UnixMilli()
	}
	_ = m.conversation.addEntry(entry)
	m.emit(protocol.Event{Type: protocol.EvtManagerEntry, Entry: &entry})
	return entry
}

func (m *Manager) emit(event protocol.Event) {
	m.emitEvent(event)
}

func (m *Manager) currentSettings() protocol.ManagerSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	settings := m.settings
	settings.KeySet = m.keySet
	return settings
}

func (m *Manager) setSettings(next *protocol.ManagerSettings) protocol.Event {
	if next == nil {
		return protocol.Event{Type: protocol.EvtError, Error: "No settings were sent."}
	}
	if err := validateSettings(*next); err != nil {
		return protocol.Event{Type: protocol.EvtError, Error: err.Error()}
	}
	if err := saveSettings(filepath.Join(m.dir, "settings.json"), *next); err != nil {
		return protocol.Event{Type: protocol.EvtError, Error: "Couldn't save the settings: " + err.Error()}
	}
	m.mu.Lock()
	m.settings = *next
	m.settings.KeySet = false
	m.mu.Unlock()
	event := m.settingsEvent()
	m.emit(event)
	return event
}

func (m *Manager) setKey(ctx context.Context, key string) protocol.Event {
	key = strings.TrimSpace(key)
	var err error
	if key == "" {
		err = m.secrets.Delete(ctx, keyName)
	} else if strings.ContainsAny(key, " \t") {
		return protocol.Event{Type: protocol.EvtError, Error: "That doesn't look like an API key."}
	} else {
		err = m.secrets.Set(ctx, keyName, key)
	}
	if err != nil {
		return protocol.Event{Type: protocol.EvtError, Error: err.Error()}
	}
	m.mu.Lock()
	m.client = nil
	m.keySet = key != ""
	m.mu.Unlock()
	if key == "" {
		m.setStatus("needs_key", "")
	} else {
		m.setStatus("idle", "")
	}
	event := m.settingsEvent()
	m.emit(event)
	return event
}

// apiKey is the stored key, or ANTHROPIC_API_KEY for a daemon run without one.
func (m *Manager) apiKey(ctx context.Context) (string, error) {
	key, err := m.secrets.Get(ctx, keyName)
	if err == nil && key != "" {
		return key, nil
	}
	if env := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); env != "" {
		return env, nil
	}
	if err == nil {
		err = secrets.ErrNotFound
	}
	return "", err
}

// model is the model client, made from the key the first time it is needed.
func (m *Manager) model(ctx context.Context) (Model, error) {
	m.mu.Lock()
	client := m.client
	m.mu.Unlock()
	if client != nil {
		return client, nil
	}
	key, err := m.apiKey(ctx)
	if err != nil {
		return nil, err
	}
	client = m.newModel(key)
	m.mu.Lock()
	m.client = client
	m.keySet = true
	m.mu.Unlock()
	return client, nil
}

func (m *Manager) setStatus(state, message string) {
	m.mu.Lock()
	m.status = protocol.ManagerStatus{State: state, Error: message, KeySet: m.keySet}
	m.mu.Unlock()
	m.emit(m.statusEvent())
}

func (m *Manager) statusEvent() protocol.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := m.status
	status.KeySet = m.keySet
	return protocol.Event{Type: protocol.EvtManagerStatus, ManagerStatus: &status}
}

func (m *Manager) settingsEvent() protocol.Event {
	settings := m.currentSettings()
	event := m.statusEvent()
	event.Type = protocol.EvtManagerSettings
	event.ManagerSettings = &settings
	return event
}

func newID() string {
	return fmt.Sprintf("m-%d-%06x", time.Now().UnixMilli(), rand.IntN(1<<24))
}

var homeOnce = sync.OnceValue(func() string {
	home, _ := os.UserHomeDir()
	return home
})

func homeDir() string { return homeOnce() }
