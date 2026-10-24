package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Agentman schedules things for itself, the way an agent schedules a task
// notification: a reminder that wakes it at a time, once or on a repeat, and
// a watch on an agent whose result comes back to it when the agent finishes
// its turn, needs the user, goes idle or ends. Either way it takes a turn of
// its own, without the user writing anything. The user sees every schedule
// and can cancel it.
//
// Schedules are kept in schedules.json, so a restart keeps them: a reminder
// that came due while the daemon was down fires once when it is back.

// Bounds on schedules. Every wake-up is a turn on the model, so a repeat
// with no end of its own stops after repeatSpan.
const (
	minEvery      = time.Minute
	maxSchedules  = 50
	repeatSpan    = 12 * time.Hour
	defaultWatch  = time.Hour
	maxWatch      = 24 * time.Hour
	maxAhead      = 7 * 24 * time.Hour
	scheduleTick  = time.Second
	nothingToSay  = "(nothing to report)"
	lastWordsSize = 1500
)

// scheduleStore is the schedules, in schedules.json.
type scheduleStore struct {
	mu    sync.Mutex
	path  string
	items map[string]protocol.ManagerSchedule
}

func loadSchedules(dir string) *scheduleStore {
	s := &scheduleStore{path: filepath.Join(dir, "schedules.json"), items: map[string]protocol.ManagerSchedule{}}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	var stored []protocol.ManagerSchedule
	if json.Unmarshal(raw, &stored) == nil {
		for _, item := range stored {
			if item.ID != "" {
				s.items[item.ID] = item
			}
		}
	}
	return s
}

// list is every schedule, soonest first.
func (s *scheduleStore) list() []protocol.ManagerSchedule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

func (s *scheduleStore) listLocked() []protocol.ManagerSchedule {
	items := make([]protocol.ManagerSchedule, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Due != items[j].Due {
			return items[i].Due < items[j].Due
		}
		return items[i].ID < items[j].ID
	})
	return items
}

func (s *scheduleStore) put(item protocol.ManagerSchedule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[item.ID]; !exists && len(s.items) >= maxSchedules {
		return fmt.Errorf("there are already %d schedules; cancel some first", maxSchedules)
	}
	s.items[item.ID] = item
	return s.saveLocked()
}

func (s *scheduleStore) remove(id string) (protocol.ManagerSchedule, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok {
		return item, false
	}
	delete(s.items, id)
	_ = s.saveLocked()
	return item, true
}

func (s *scheduleStore) saveLocked() error {
	raw, err := json.MarshalIndent(s.listLocked(), "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// wakeup is a turn Agentman takes on its own: the line the user sees, and
// what the model is told.
type wakeup struct {
	entry  string
	tool   string
	prompt string
}

// remind schedules a reminder. at is when it first fires; every, times and
// until make it repeat.
func (m *Manager) remind(text string, at time.Time, every time.Duration, times int, until time.Time) (protocol.ManagerSchedule, error) {
	text = strings.TrimSpace(text)
	now := m.now()
	switch {
	case text == "":
		return protocol.ManagerSchedule{}, errors.New("say what the reminder is for")
	case at.Before(now.Add(-time.Second)):
		return protocol.ManagerSchedule{}, errors.New("that time has passed")
	case at.After(now.Add(maxAhead)):
		return protocol.ManagerSchedule{}, errors.New("reminders go at most a week ahead")
	case every != 0 && every < minEvery:
		return protocol.ManagerSchedule{}, errors.New("a repeat is at least a minute apart")
	case times < 0:
		return protocol.ManagerSchedule{}, errors.New("times is how many times it fires, at least 1")
	}
	item := protocol.ManagerSchedule{
		ID: "s-" + newID()[2:], Kind: protocol.ScheduleReminder, Text: clip(text, 500),
		Due: at.UnixMilli(), CreatedAt: now.UnixMilli(),
	}
	if every > 0 {
		item.Every = int(every / time.Second)
		item.Times = times
		switch {
		case !until.IsZero():
			item.Until = until.UnixMilli()
		case times == 0:
			// A repeat with no end of its own is not left to run up a bill.
			item.Until = at.Add(repeatSpan).UnixMilli()
		}
	}
	if err := m.schedules.put(item); err != nil {
		return protocol.ManagerSchedule{}, err
	}
	m.emitSchedules()
	return item, nil
}

// watch schedules a watch on an agent: its result comes back when the agent
// reaches the condition, or when timeout passes. A condition that already
// holds comes back at once.
func (m *Manager) watch(session protocol.Session, condition, note string, timeout time.Duration) (protocol.ManagerSchedule, error) {
	switch condition {
	case protocol.WatchTurnEnd, protocol.WatchNeedsUser, protocol.WatchIdle, protocol.WatchEnded:
	default:
		return protocol.ManagerSchedule{}, errors.New("watch until turn_end, needs_user, idle or ended")
	}
	if timeout <= 0 {
		timeout = defaultWatch
	}
	if timeout > maxWatch {
		return protocol.ManagerSchedule{}, errors.New("a watch waits at most a day")
	}
	now := m.now()
	item := protocol.ManagerSchedule{
		ID: "w-" + newID()[2:], Kind: protocol.ScheduleWatch, Text: clip(strings.TrimSpace(note), 500),
		Due: now.Add(timeout).UnixMilli(), SessionID: session.ID, AgentName: session.Name,
		Condition: condition, CreatedAt: now.UnixMilli(),
	}
	if err := m.schedules.put(item); err != nil {
		return protocol.ManagerSchedule{}, err
	}
	m.emitSchedules()
	// Already there: the result is ready now.
	switch {
	case condition == protocol.WatchIdle && session.State == protocol.StateIdle,
		condition == protocol.WatchNeedsUser && session.Question != nil:
		m.finishWatch(context.Background(), item, "it already was when you started watching")
	}
	return item, nil
}

// scheduleChange is what update_schedule changes; unset fields stay.
type scheduleChange struct {
	text    *string
	at      *time.Time
	every   *time.Duration
	times   *int
	until   *time.Time
	timeout *time.Duration
}

// updateSchedule changes a schedule in place: a reminder's time, repeat or
// text, or a watch's timeout or note.
func (m *Manager) updateSchedule(id string, change scheduleChange) (protocol.ManagerSchedule, error) {
	m.schedules.mu.Lock()
	item, ok := m.schedules.items[strings.TrimSpace(id)]
	m.schedules.mu.Unlock()
	if !ok {
		return item, errors.New("nothing is scheduled with that id; it may have fired already")
	}
	now := m.now()
	if change.text != nil {
		text := strings.TrimSpace(*change.text)
		if text == "" && item.Kind == protocol.ScheduleReminder {
			return item, errors.New("a reminder needs to say what it is for")
		}
		item.Text = clip(text, 500)
	}
	switch item.Kind {
	case protocol.ScheduleReminder:
		if change.timeout != nil {
			return item, errors.New("a reminder has no timeout; change its time instead")
		}
		if change.at != nil {
			if change.at.Before(now.Add(-time.Second)) || change.at.After(now.Add(maxAhead)) {
				return item, errors.New("the new time must be from now to a week ahead")
			}
			item.Due = change.at.UnixMilli()
		}
		if change.every != nil {
			if *change.every != 0 && *change.every < minEvery {
				return item, errors.New("a repeat is at least a minute apart")
			}
			item.Every = int(*change.every / time.Second)
		}
		if change.times != nil {
			item.Times = *change.times
		}
		if change.until != nil {
			item.Until = change.until.UnixMilli()
		}
		if item.Every == 0 {
			item.Times, item.Until = 0, 0
		} else if item.Times == 0 && item.Until == 0 {
			item.Until = time.UnixMilli(item.Due).Add(repeatSpan).UnixMilli()
		}
	case protocol.ScheduleWatch:
		if change.at != nil || change.every != nil || change.times != nil || change.until != nil {
			return item, errors.New("a watch has a timeout, not a time or a repeat")
		}
		if change.timeout != nil {
			if *change.timeout <= 0 || *change.timeout > maxWatch {
				return item, errors.New("a watch waits from a minute to a day")
			}
			item.Due = now.Add(*change.timeout).UnixMilli()
		}
	}
	if err := m.schedules.put(item); err != nil {
		return item, err
	}
	m.emitSchedules()
	return item, nil
}

// cancelSchedule removes a schedule, by the user or by Agentman.
func (m *Manager) cancelSchedule(id string) (protocol.ManagerSchedule, error) {
	item, ok := m.schedules.remove(strings.TrimSpace(id))
	if !ok {
		return item, errors.New("nothing is scheduled with that id; it may have fired already")
	}
	m.emitSchedules()
	return item, nil
}

func (m *Manager) emitSchedules() {
	m.emit(protocol.Event{Type: protocol.EvtManagerSchedules, Schedules: m.schedules.list()})
}

// runSchedules fires what comes due until ctx ends.
func (m *Manager) runSchedules(ctx context.Context) {
	ticker := time.NewTicker(scheduleTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.fireDue(ctx, m.now())
		}
	}
}

// fireDue fires every reminder that is due at now, and gives up on every
// watch whose time is up.
func (m *Manager) fireDue(ctx context.Context, now time.Time) {
	for _, item := range m.schedules.list() {
		if item.Due > now.UnixMilli() {
			break
		}
		switch item.Kind {
		case protocol.ScheduleReminder:
			m.fireReminder(item, now)
		case protocol.ScheduleWatch:
			waited := time.Duration(item.Due-item.CreatedAt) * time.Millisecond
			m.finishWatch(ctx, item, fmt.Sprintf("you stopped waiting after %s, and it hadn't", roundDuration(waited)))
		}
	}
}

// fireReminder wakes Agentman with a reminder, and schedules its next time
// when it repeats.
func (m *Manager) fireReminder(item protocol.ManagerSchedule, now time.Time) {
	item.Fired++
	next := time.Time{}
	if item.Every > 0 {
		every := time.Duration(item.Every) * time.Second
		next = time.UnixMilli(item.Due).Add(every)
		// Missed while the daemon was down: once now, then on from here.
		if !next.After(now) {
			next = now.Add(every)
		}
		if (item.Times > 0 && item.Fired >= item.Times) || (item.Until > 0 && next.UnixMilli() > item.Until) {
			next = time.Time{}
		}
	}
	repeat := ""
	if next.IsZero() {
		m.schedules.remove(item.ID)
		if item.Every > 0 {
			repeat = " This was its last time."
		}
	} else {
		item.Due = next.UnixMilli()
		_ = m.schedules.put(item)
		repeat = fmt.Sprintf(" It repeats every %s; next at %s.", roundDuration(time.Duration(item.Every)*time.Second), next.Format("15:04"))
	}
	m.emitSchedules()
	set := time.UnixMilli(item.CreatedAt).Format("15:04")
	m.wake(wakeup{
		entry: "Reminder: " + item.Text,
		tool:  "reminder",
		prompt: fmt.Sprintf("<scheduled kind=\"reminder\" id=%q>\nThis is one of your own reminders, not a message from the user. You set it at %s: %q.%s\n"+
			"Act on it now: look at what it is about, and tell the user what is worth telling. "+
			"If nothing is, reply with exactly %s and the user won't be disturbed.\n</scheduled>",
			item.ID, set, item.Text, repeat, nothingToSay),
	})
}

// finishWatch ends a watch and wakes Agentman with what happened.
func (m *Manager) finishWatch(ctx context.Context, item protocol.ManagerSchedule, happened string) {
	if _, ok := m.schedules.remove(item.ID); !ok {
		return
	}
	m.emitSchedules()
	what := conditionText(item.Condition)
	var b strings.Builder
	fmt.Fprintf(&b, "<task-result kind=\"watch\" id=%q agent=%q>\nYour watch on %s until %s is done: %s (%s).\n",
		item.ID, item.AgentName, item.AgentName, what, happened, m.now().Format("15:04"))
	if item.Text != "" {
		fmt.Fprintf(&b, "You started it because: %s\n", item.Text)
	}
	if session, ok := m.session(item.SessionID); ok {
		fmt.Fprintf(&b, "Its state now: %s.\n", stateText(session))
		if brief, ok := m.briefs.get(session.ID); ok && brief.Summary != "" {
			fmt.Fprintf(&b, "Your brief on it: %s\n", brief.Summary)
		}
		if last := m.lastWords(ctx, session.ID); last != "" {
			fmt.Fprintf(&b, "Its latest message: %s\n", last)
		}
	} else {
		b.WriteString("It is no longer running.\n")
	}
	fmt.Fprintf(&b, "Act on it now, and tell the user what is worth telling. If nothing is, reply with exactly %s.\n</task-result>", nothingToSay)
	m.wake(wakeup{
		entry:  fmt.Sprintf("Watch: %s %s", item.AgentName, watchOutcome(item.Condition, happened)),
		tool:   "watch",
		prompt: b.String(),
	})
}

// watchEvent hears an agent reach a state, and finishes the watches that
// were waiting for it.
func (m *Manager) watchEvent(sessionID string, conditions ...string) {
	for _, item := range m.schedules.list() {
		if item.Kind != protocol.ScheduleWatch || item.SessionID != sessionID {
			continue
		}
		for _, condition := range conditions {
			if item.Condition == condition {
				m.finishWatch(context.Background(), item, "it happened")
				break
			}
		}
	}
}

// watchSessions finishes the watches whose agent has ended or gone idle.
func (m *Manager) watchSessionList(sessions []protocol.Session) {
	byID := make(map[string]protocol.Session, len(sessions))
	for _, s := range sessions {
		byID[s.ID] = s
	}
	for _, item := range m.schedules.list() {
		if item.Kind != protocol.ScheduleWatch {
			continue
		}
		s, live := byID[item.SessionID]
		switch {
		case !live:
			if item.Condition == protocol.WatchEnded {
				m.finishWatch(context.Background(), item, "it happened")
			} else {
				m.finishWatch(context.Background(), item, "the agent ended first")
			}
		case item.Condition == protocol.WatchIdle && s.State == protocol.StateIdle:
			m.finishWatch(context.Background(), item, "it happened")
		}
	}
}

// lastWords is the agent's latest reply, clipped.
func (m *Manager) lastWords(ctx context.Context, sessionID string) string {
	page := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqFetchMessages, SessionID: sessionID, Limit: 12}).Page
	if page == nil {
		return ""
	}
	for i := len(page.Messages) - 1; i >= 0; i-- {
		message := page.Messages[i]
		if message.Role == protocol.RoleAssistant && strings.TrimSpace(message.Text) != "" && !message.IsSidechain {
			return clip(strings.TrimSpace(message.Text), lastWordsSize)
		}
	}
	return ""
}

// wake has Agentman take a turn on its own: now, or after the reply it is
// writing.
func (m *Manager) wake(w wakeup) {
	m.mu.Lock()
	m.wakeups = append(m.wakeups, w)
	m.mu.Unlock()
	m.startWakeup()
}

// startWakeup takes the next wake-up's turn, if one is waiting and nothing
// else is being written. Without a key, it is shown and dropped.
func (m *Manager) startWakeup() {
	m.mu.Lock()
	if m.stopTurn != nil || len(m.wakeups) == 0 {
		m.mu.Unlock()
		return
	}
	w := m.wakeups[0]
	m.wakeups = m.wakeups[1:]
	m.mu.Unlock()

	model, err := m.model(context.Background())
	if err != nil {
		m.addEntry(protocol.ManagerEntry{Role: "scheduled", Text: w.entry + " (not acted on: Agentman has no API key)", Tool: w.tool})
		m.startWakeup()
		return
	}
	m.mu.Lock()
	if m.stopTurn != nil {
		// The user started a reply in between: this one waits for it.
		m.wakeups = append([]wakeup{w}, m.wakeups...)
		m.mu.Unlock()
		return
	}
	turnCtx, cancel := context.WithCancel(context.Background())
	m.stopTurn = cancel
	m.mu.Unlock()

	m.addEntry(protocol.ManagerEntry{Role: "scheduled", Text: w.entry, Tool: w.tool})
	go m.runTurn(turnCtx, model, turnInput{text: w.prompt})
}

func conditionText(condition string) string {
	switch condition {
	case protocol.WatchTurnEnd:
		return "its turn ended"
	case protocol.WatchNeedsUser:
		return "it needed the user"
	case protocol.WatchIdle:
		return "it was idle"
	case protocol.WatchEnded:
		return "it ended"
	}
	return condition
}

func watchOutcome(condition, happened string) string {
	if happened != "it happened" && happened != "it already was when you started watching" {
		return "— " + happened
	}
	switch condition {
	case protocol.WatchTurnEnd:
		return "finished its turn"
	case protocol.WatchNeedsUser:
		return "needs you"
	case protocol.WatchIdle:
		return "is idle"
	case protocol.WatchEnded:
		return "ended"
	}
	return "is done"
}

// roundDuration says a duration in whole minutes or hours.
func roundDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour || d%time.Hour != 0:
		minutes := int(d.Round(time.Minute) / time.Minute)
		if minutes == 1 {
			return "1 minute"
		}
		if minutes >= 120 && minutes%60 == 0 {
			return fmt.Sprintf("%d hours", minutes/60)
		}
		return fmt.Sprintf("%d minutes", minutes)
	case d == time.Hour:
		return "1 hour"
	default:
		return fmt.Sprintf("%d hours", int(d/time.Hour))
	}
}
