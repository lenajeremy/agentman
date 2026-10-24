package manager

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// clock is a time the test moves by hand.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

func scheduleHarness(t *testing.T, replies ...string) (*harness, *clock) {
	t.Helper()
	h := newHarness(t, &scriptedModel{replies: replies}, true)
	c := &clock{now: time.Date(2026, 10, 24, 18, 0, 0, 0, time.Local)}
	h.m.now = c.Now
	return h, c
}

// lastPrompt is the text of the last message the model was sent.
func (h *harness) lastPrompt(t *testing.T) string {
	t.Helper()
	calls := h.model.calls()
	if calls == 0 {
		t.Fatal("the model was never called")
	}
	messages := h.model.request(calls - 1).Messages
	var b strings.Builder
	for _, block := range messages[len(messages)-1].Content {
		if block.OfText != nil {
			b.WriteString(block.OfText.Text)
		}
	}
	return b.String()
}

func (h *harness) waitCalls(t *testing.T, n int) {
	t.Helper()
	waitFor(t, "the model to be called", func() bool { return h.model.calls() >= n })
	h.waitIdle(t)
}

// A reminder fires at its time, and Agentman takes a turn with it on its
// own, the user seeing a quiet line for it.
func TestAReminderWakesAgentman(t *testing.T) {
	h, c := scheduleHarness(t, textReply("agentman-58 has finished; nothing needs you."))
	item, err := h.m.remind("check agentman-58", c.Now().Add(10*time.Minute), 0, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	h.m.fireDue(context.Background(), c.advance(9*time.Minute))
	if h.model.calls() != 0 {
		t.Fatal("it fired early")
	}
	h.m.fireDue(context.Background(), c.advance(time.Minute))
	h.waitCalls(t, 1)

	if prompt := h.lastPrompt(t); !strings.Contains(prompt, "<scheduled") || !strings.Contains(prompt, "check agentman-58") ||
		!strings.Contains(prompt, "not a message from the user") {
		t.Fatalf("prompt = %s", prompt)
	}
	roles := roles(h.entries())
	if len(roles) < 2 || roles[0] != "scheduled" || roles[len(roles)-1] != "assistant" {
		t.Fatalf("entries = %v", roles)
	}
	if len(h.m.schedules.list()) != 0 || item.ID == "" {
		t.Fatal("a one-off reminder stayed scheduled")
	}
}

// A repeat comes back on its interval, and ends after its times.
func TestARepeatEndsAfterItsTimes(t *testing.T) {
	h, c := scheduleHarness(t, textReply(nothingToSay), textReply(nothingToSay), textReply(nothingToSay))
	if _, err := h.m.remind("brief the user", c.Now().Add(5*time.Minute), 5*time.Minute, 2, time.Time{}); err != nil {
		t.Fatal(err)
	}
	h.m.fireDue(context.Background(), c.advance(5*time.Minute))
	h.waitCalls(t, 1)
	if items := h.m.schedules.list(); len(items) != 1 || items[0].Fired != 1 || items[0].Due != c.Now().Add(5*time.Minute).UnixMilli() {
		t.Fatalf("after one: %+v", items)
	}
	h.m.fireDue(context.Background(), c.advance(5*time.Minute))
	h.waitCalls(t, 2)
	if items := h.m.schedules.list(); len(items) != 0 {
		t.Fatalf("a repeat outlived its times: %+v", items)
	}
	h.m.fireDue(context.Background(), c.advance(time.Hour))
	time.Sleep(20 * time.Millisecond)
	if h.model.calls() != 2 {
		t.Fatalf("it fired %d times", h.model.calls())
	}
	// Nothing to say stays a quiet line, not a reply.
	for _, entry := range h.entries() {
		if entry.Role == "assistant" {
			t.Fatalf("an empty wake-up became a reply: %+v", entry)
		}
	}
}

// A repeat with no end of its own stops after twelve hours.
func TestAnEndlessRepeatStopsAfterTwelveHours(t *testing.T) {
	h, c := scheduleHarness(t)
	item, err := h.m.remind("brief the user", c.Now().Add(30*time.Minute), 30*time.Minute, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if want := c.Now().Add(30*time.Minute + 12*time.Hour).UnixMilli(); item.Until != want {
		t.Fatalf("until = %v, want %v", time.UnixMilli(item.Until), time.UnixMilli(want))
	}
	for _, bad := range []struct {
		at    time.Time
		every time.Duration
	}{{c.Now().Add(-time.Hour), 0}, {c.Now().Add(8 * 24 * time.Hour), 0}, {c.Now().Add(time.Minute), 10 * time.Second}} {
		if _, err := h.m.remind("x", bad.at, bad.every, 0, time.Time{}); err == nil {
			t.Errorf("scheduled %+v", bad)
		}
	}
}

// A reminder that comes due while Agentman is replying waits for the
// reply, then takes its turn.
func TestAWakeUpWaitsForTheReply(t *testing.T) {
	model := &scriptedModel{replies: []string{textReply("Here's the answer."), textReply("Reminder handled.")}}
	h := newHarness(t, model, true)
	c := &clock{now: time.Now()}
	h.m.now = c.Now
	h.m.mu.Lock()
	h.m.stopTurn = func() {}
	h.m.mu.Unlock()
	h.m.wake(wakeup{entry: "Reminder: x", tool: "reminder", prompt: "<scheduled>x</scheduled>"})
	if model.calls() != 0 {
		t.Fatal("a wake-up ran during a reply")
	}
	h.m.mu.Lock()
	h.m.stopTurn = nil
	h.m.mu.Unlock()
	h.m.send(context.Background(), "what's up?")
	h.waitCalls(t, 2)
	if !strings.Contains(h.lastPrompt(t), "<scheduled>x</scheduled>") {
		t.Fatalf("the wake-up didn't follow the reply: %s", h.lastPrompt(t))
	}
}

// A watch brings back what happened when the agent's turn ends, with its
// latest words.
func TestAWatchBringsBackTheResult(t *testing.T) {
	h, _ := scheduleHarness(t, textReply("agentman-58 finished the fonts."))
	h.agents.answer = func(req protocol.Request) protocol.Event {
		return protocol.Event{Type: protocol.EvtPage, Page: &protocol.Page{SessionID: req.SessionID, Messages: []protocol.Message{
			{ID: "1", Role: protocol.RoleAssistant, Ts: 1, Text: "Fonts are in; 17 tests pass."},
		}}}
	}
	site := h.agents.Sessions()[0]
	if _, err := h.m.watch(site, protocol.WatchTurnEnd, "check the fonts landed", 0); err != nil {
		t.Fatal(err)
	}
	h.m.TurnComplete(site)
	h.waitCalls(t, 1)
	prompt := h.lastPrompt(t)
	if !strings.Contains(prompt, "<task-result") || !strings.Contains(prompt, "its turn ended") ||
		!strings.Contains(prompt, "check the fonts landed") || !strings.Contains(prompt, "Fonts are in; 17 tests pass.") {
		t.Fatalf("prompt = %s", prompt)
	}
	if entries := h.entries(); entries[0].Role != "scheduled" || entries[0].Text != "Watch: site finished its turn" {
		t.Fatalf("entries = %+v", entries)
	}
}

// A watch that waits too long says so; one on an agent that ends says
// that.
func TestAWatchGivesUpOrSeesTheAgentEnd(t *testing.T) {
	h, c := scheduleHarness(t, textReply(nothingToSay), textReply(nothingToSay))
	site := h.agents.Sessions()[0]
	if _, err := h.m.watch(site, protocol.WatchTurnEnd, "", 30*time.Minute); err != nil {
		t.Fatal(err)
	}
	h.m.fireDue(context.Background(), c.advance(30*time.Minute))
	h.waitCalls(t, 1)
	if prompt := h.lastPrompt(t); !strings.Contains(prompt, "stopped waiting after 30 minutes") {
		t.Fatalf("prompt = %s", prompt)
	}

	if _, err := h.m.watch(site, protocol.WatchTurnEnd, "", 0); err != nil {
		t.Fatal(err)
	}
	h.m.watchSessionList(h.agents.Sessions()[1:])
	h.waitCalls(t, 2)
	if prompt := h.lastPrompt(t); !strings.Contains(prompt, "the agent ended first") {
		t.Fatalf("prompt = %s", prompt)
	}
}

// Waiting for something already true comes back at once.
func TestAWatchOnWhatIsAlreadyTrueComesBackAtOnce(t *testing.T) {
	h, _ := scheduleHarness(t, textReply("api needs you."))
	api := h.agents.Sessions()[1]
	if _, err := h.m.watch(api, protocol.WatchNeedsUser, "", 0); err != nil {
		t.Fatal(err)
	}
	h.waitCalls(t, 1)
	if !strings.Contains(h.lastPrompt(t), "already was when you started watching") || len(h.m.schedules.list()) != 0 {
		t.Fatalf("prompt = %s", h.lastPrompt(t))
	}
}

// Schedules survive a restart; the user can list and cancel them.
func TestSchedulesSurviveARestartAndCanBeCancelled(t *testing.T) {
	h, c := scheduleHarness(t)
	item, _ := h.m.remind("check in", c.Now().Add(time.Hour), 0, 0, time.Time{})
	again := h.newManager(t)
	listed := again.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSchedules})
	if len(listed.Schedules) != 1 || listed.Schedules[0].ID != item.ID {
		t.Fatalf("after a restart: %+v", listed)
	}
	cancelled := again.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerScheduleCancel, ScheduleID: item.ID})
	if cancelled.Type != protocol.EvtManagerSchedules || len(cancelled.Schedules) != 0 {
		t.Fatalf("cancel = %+v", cancelled)
	}
	if again.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerScheduleCancel, ScheduleID: item.ID}).Type != protocol.EvtError {
		t.Fatal("cancelling twice wasn't refused")
	}
}

// Agentman schedules through its tools, and is told about the ones the
// user cancels.
func TestAgentmanSchedulesThroughItsTools(t *testing.T) {
	h, _ := scheduleHarness(t,
		toolReply("t1", "schedule_reminder", `{"message":"check agentman-58","in_minutes":10,"every_minutes":10,"times":3}`),
		toolReply("t2", "watch_agent", `{"agent":"site","until":"turn_end","note":"see what changed"}`),
		textReply("Done."),
	)
	h.m.send(context.Background(), "keep an eye on things")
	h.waitIdle(t)
	items := h.m.schedules.list()
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	var shown []string
	for _, entry := range h.entries() {
		if entry.Role == "activity" {
			shown = append(shown, entry.Text)
		}
	}
	if len(shown) != 2 || !strings.HasPrefix(shown[0], "Set a reminder: “check agentman-58” at 18:10, every 10 minutes, 3 more") ||
		!strings.HasPrefix(shown[1], "Watching site until its turn ended") {
		t.Fatalf("shown = %q", shown)
	}
}

func TestTimesAreReadAsLocal(t *testing.T) {
	now := time.Date(2026, 10, 24, 18, 0, 0, 0, time.Local)
	if at, _ := parseWhen("18:30", now); !at.Equal(now.Add(30 * time.Minute)) {
		t.Errorf("18:30 = %v", at)
	}
	if at, _ := parseWhen("09:00", now); !at.Equal(time.Date(2026, 10, 25, 9, 0, 0, 0, time.Local)) {
		t.Errorf("09:00 = %v, want tomorrow", at)
	}
	if _, err := parseWhen("soonish", now); err == nil {
		t.Error("read nonsense as a time")
	}
}
