package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// The overnight queue: work lined up in the evening, started in the night a
// few at a time and one to a folder, and reported in the morning. Only what
// the user queued is started, and agents keep to the trust of their folder.

const (
	defaultNightStart  = "23:00"
	defaultNightEnd    = "07:00"
	defaultNightAgents = 2
	maxNightAgents     = 6
	maxQueue           = 100
	queueSettle        = 15 * time.Second
	queueNoShow        = time.Minute
)

var clockTime = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

type queueStore struct {
	path string
	mu   sync.Mutex
	list []protocol.QueueItem
}

func loadQueue(dir string) *queueStore {
	store := &queueStore{path: filepath.Join(dir, "queue.json")}
	if raw, err := os.ReadFile(store.path); err == nil {
		_ = json.Unmarshal(raw, &store.list)
	}
	return store
}

func (q *queueStore) saveLocked() {
	raw, err := json.MarshalIndent(q.list, "", "  ")
	if err != nil || os.MkdirAll(filepath.Dir(q.path), 0o700) != nil {
		return
	}
	tmp := q.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, q.path)
	}
}

func (q *queueStore) all() []protocol.QueueItem {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.list)
}

func (q *queueStore) add(item protocol.QueueItem) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.list) >= maxQueue {
		return fmt.Errorf("the queue holds %d at most", maxQueue)
	}
	q.list = append(q.list, item)
	q.saveLocked()
	return nil
}

// remove takes out an item that hasn't started.
func (q *queueStore) remove(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	before := len(q.list)
	q.list = slices.DeleteFunc(q.list, func(item protocol.QueueItem) bool {
		return item.ID == id && item.State == protocol.QueueWaiting
	})
	if len(q.list) == before {
		return false
	}
	q.saveLocked()
	return true
}

func (q *queueStore) change(id string, edit func(*protocol.QueueItem)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.list {
		if q.list[i].ID == id {
			edit(&q.list[i])
			q.saveLocked()
			return
		}
	}
}

// clear drops what has been reported.
func (q *queueStore) clearReported() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.list = slices.DeleteFunc(q.list, func(item protocol.QueueItem) bool { return item.Reported })
	q.saveLocked()
}

func (m *Manager) emitQueue() {
	m.emit(protocol.Event{Type: protocol.EvtManagerQueue, Queue: m.queue.all()})
}

// queueWork adds a piece of work for tonight, from the app or the model.
func (m *Manager) queueWork(agent, folder, task string) (protocol.QueueItem, error) {
	agent = strings.ToLower(strings.TrimSpace(agent))
	task = strings.TrimSpace(task)
	if _, ok := launchKinds[agent]; !ok {
		return protocol.QueueItem{}, fmt.Errorf("choose an agent: %s", strings.Join(launchKindNames, ", "))
	}
	if task == "" || len(task) > maxTaskText {
		return protocol.QueueItem{}, errors.New("say what it should do, in a few paragraphs at most")
	}
	resolved, _, err := m.launchFolder(folder)
	if err != nil {
		return protocol.QueueItem{}, err
	}
	item := protocol.QueueItem{ID: newID(), Agent: agent, Folder: resolved, Task: task, State: protocol.QueueWaiting, CreatedAt: m.now().UnixMilli()}
	if err := m.queue.add(item); err != nil {
		return protocol.QueueItem{}, err
	}
	m.emitQueue()
	return item, nil
}

func runQueueTask(_ context.Context, m *Manager, input map[string]any) toolResult {
	agent, _ := input["agent"].(string)
	folder, _ := input["folder"].(string)
	task, _ := input["task"].(string)
	item, err := m.queueWork(agent, folder, task)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	if autopilot := m.currentSettings().Autopilot; autopilot == nil || !autopilot.Overnight {
		return toolResult{text: "Queued, but the overnight queue is switched off in Settings, so it won't start until the user turns it on.", activity: "Queued for tonight: " + clip(item.Task, 100)}
	}
	return toolResult{text: "Queued for tonight.", activity: "Queued for tonight: " + clip(item.Task, 100)}
}

func (m *Manager) handleQueueRequest(req protocol.Request) protocol.Event {
	switch req.Type {
	case protocol.ReqManagerQueueAdd:
		if req.Queue == nil {
			return protocol.Event{Type: protocol.EvtError, Error: "Nothing to queue."}
		}
		if _, err := m.queueWork(req.Queue.Agent, req.Queue.Folder, req.Queue.Task); err != nil {
			return protocol.Event{Type: protocol.EvtError, Error: err.Error()}
		}
	case protocol.ReqManagerQueueRemove:
		if !m.queue.remove(req.QueueID) {
			return protocol.Event{Type: protocol.EvtError, Error: "That has already started."}
		}
		m.emitQueue()
	}
	return protocol.Event{Type: protocol.EvtManagerQueue, Queue: m.queue.all()}
}

// nightWindow is the night's start and end, and whether now is inside it.
func nightWindow(autopilot *protocol.Autopilot, now time.Time) (inside bool) {
	start, end := minuteOfDay(autopilot.NightStart, defaultNightStart), minuteOfDay(autopilot.NightEnd, defaultNightEnd)
	current := now.Hour()*60 + now.Minute()
	if start <= end {
		return current >= start && current < end
	}
	return current >= start || current < end
}

func minuteOfDay(value, fallback string) int {
	if !clockTime.MatchString(value) {
		value = fallback
	}
	hour, _ := strconv.Atoi(value[:2])
	minute, _ := strconv.Atoi(value[3:])
	return hour*60 + minute
}

func validateAutopilot(autopilot *protocol.Autopilot) error {
	if autopilot == nil {
		return nil
	}
	for _, value := range []string{autopilot.NightStart, autopilot.NightEnd} {
		if value != "" && !clockTime.MatchString(value) {
			return fmt.Errorf("a time is HH:MM on the 24-hour clock, not %q", value)
		}
	}
	if autopilot.NightAgents < 0 || autopilot.NightAgents > maxNightAgents {
		return fmt.Errorf("the night queue runs at most %d agents at once", maxNightAgents)
	}
	return nil
}

// queueTurnEnded notes that a queued agent finished a turn.
func (m *Manager) queueTurnEnded(sessionID string) {
	now := m.now().UnixMilli()
	for _, item := range m.queue.all() {
		if item.SessionID == sessionID && item.State == protocol.QueueRunning {
			m.queue.change(item.ID, func(stored *protocol.QueueItem) { stored.TurnEndedAt = now })
		}
	}
}

// runOvernight settles what is running, starts what it can while it is
// night, and writes the morning's report once it is over.
func (m *Manager) runOvernight(ctx context.Context, settings protocol.ManagerSettings) {
	autopilot := settings.Autopilot
	if autopilot == nil || !autopilot.Overnight {
		return
	}
	m.queueMu.Lock()
	defer m.queueMu.Unlock()
	changed := false
	for _, item := range m.queue.all() {
		if item.State == protocol.QueueRunning && m.settleQueued(item) {
			changed = true
		}
	}
	night := nightWindow(autopilot, m.now())
	if night {
		changed = m.startQueued(ctx, autopilot) || changed
	} else if m.reportNight() {
		changed = true
	}
	if changed {
		m.emitQueue()
	}
}

func (m *Manager) settleQueued(item protocol.QueueItem) bool {
	session, live := m.session(item.SessionID)
	var state, problem string
	switch {
	case !live && item.TurnEndedAt > 0:
		state = protocol.QueueDone
	case !live && m.now().Sub(time.UnixMilli(item.StartedAt)) > queueNoShow:
		state, problem = protocol.QueueFailed, "It stopped before finishing a turn"
	case live && item.TurnEndedAt > 0 && session.State == protocol.StateIdle && session.Question == nil &&
		m.now().Sub(time.UnixMilli(item.TurnEndedAt)) >= queueSettle:
		state = protocol.QueueDone
	default:
		return false
	}
	m.queue.change(item.ID, func(stored *protocol.QueueItem) { stored.State, stored.Problem = state, problem })
	return true
}

func (m *Manager) startQueued(ctx context.Context, autopilot *protocol.Autopilot) bool {
	limit := autopilot.NightAgents
	if limit == 0 {
		limit = defaultNightAgents
	}
	items := m.queue.all()
	running, busyFolders := 0, map[string]bool{}
	for _, item := range items {
		if item.State == protocol.QueueRunning {
			running++
			busyFolders[item.Folder] = true
		}
	}
	changed := false
	for _, item := range items {
		if running >= limit {
			break
		}
		if item.State != protocol.QueueWaiting || busyFolders[item.Folder] {
			continue
		}
		sessionID, err := m.launch(ctx, item.Agent, item.Folder, item.Task)
		state, problem := protocol.QueueRunning, ""
		if err != nil {
			state, problem = protocol.QueueFailed, err.Error()
		} else {
			running++
			busyFolders[item.Folder] = true
		}
		started := m.now().UnixMilli()
		m.queue.change(item.ID, func(stored *protocol.QueueItem) {
			stored.State, stored.Problem, stored.SessionID, stored.StartedAt = state, problem, sessionID, started
		})
		changed = true
	}
	return changed
}

// reportNight tells the user how the night went, once.
func (m *Manager) reportNight() bool {
	var done, failed, waiting int
	var lines []string
	for _, item := range m.queue.all() {
		switch item.State {
		case protocol.QueueDone:
			done++
			lines = append(lines, "- "+m.queueOutcome(item))
		case protocol.QueueFailed:
			failed++
			lines = append(lines, "- "+clip(item.Task, 80)+": "+item.Problem)
		case protocol.QueueRunning:
			return false
		case protocol.QueueWaiting:
			waiting++
		}
	}
	if done+failed == 0 {
		return false
	}
	text := fmt.Sprintf("Overnight: %d of %d finished.", done, done+failed)
	if waiting > 0 {
		text += fmt.Sprintf(" %d didn't get started.", waiting)
	}
	text += "\n\n" + strings.Join(lines, "\n")
	m.addEntry(protocol.ManagerEntry{Role: "brief", Text: text})
	m.note(text)
	for _, item := range m.queue.all() {
		if item.State == protocol.QueueDone || item.State == protocol.QueueFailed {
			m.queue.change(item.ID, func(stored *protocol.QueueItem) { stored.Reported = true })
		}
	}
	m.queue.clearReported()
	return true
}

func (m *Manager) queueOutcome(item protocol.QueueItem) string {
	line := clip(item.Task, 80)
	if check, ok := m.verifies.get(item.SessionID); ok && check.Summary != "" {
		return line + ": " + check.Summary
	}
	if brief, ok := m.briefs.get(item.SessionID); ok && brief.Summary != "" {
		return line + ": " + brief.Summary
	}
	return line + ": done"
}

// launch starts an agent in a folder with its first message.
func (m *Manager) launch(ctx context.Context, agent, folder, text string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(home, folder)
	if err != nil {
		return "", err
	}
	started := m.agents.Request(ctx, protocol.Request{
		Type: protocol.ReqStartSession, Kind: launchKinds[agent], Path: rel, Text: text, ClientID: "agentman-" + newID(),
	})
	if started.Type == protocol.EvtError || started.SessionID == "" {
		return "", errors.New("Couldn't start it: " + plainError(started.Error))
	}
	return started.SessionID, nil
}
