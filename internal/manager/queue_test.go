package manager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

type queueRig struct {
	h      *harness
	now    time.Time
	home   string
	starts []protocol.Request
}

func at(hour, minute int) time.Time {
	return time.Date(2026, 11, 8, hour, minute, 0, 0, time.Local)
}

func newQueueRig(t *testing.T, autopilot protocol.Autopilot) *queueRig {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{"api", "site", "docs"} {
		if err := os.MkdirAll(filepath.Join(home, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	h := newHarness(t, &scriptedModel{}, true)
	r := &queueRig{h: h, now: at(23, 30), home: home}
	h.m.now = func() time.Time { return r.now }
	h.agents.setSessions(nil)
	h.agents.answer = func(req protocol.Request) protocol.Event {
		if req.Type == protocol.ReqStartSession {
			r.starts = append(r.starts, req)
			return protocol.Event{SessionID: "s" + string(rune('0'+len(r.starts)))}
		}
		return protocol.Event{}
	}
	h.m.mu.Lock()
	h.m.settings.Autopilot = &autopilot
	h.m.mu.Unlock()
	return r
}

func (r *queueRig) add(t *testing.T, folder, task string) protocol.QueueItem {
	t.Helper()
	item, err := r.h.m.queueWork("claude", filepath.Join(r.home, folder), task)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func (r *queueRig) tick() { r.h.m.runOvernight(context.Background(), r.h.m.currentSettings()) }

func TestNothingStartsUntilTurnedOnOrBeforeNight(t *testing.T) {
	r := newQueueRig(t, protocol.Autopilot{})
	r.add(t, "api", "Do it")
	r.tick()
	if len(r.starts) != 0 {
		t.Fatal("started while off")
	}
	r = newQueueRig(t, protocol.Autopilot{Overnight: true})
	r.now = at(15, 0)
	r.add(t, "api", "Do it")
	r.tick()
	if len(r.starts) != 0 {
		t.Fatal("started in the afternoon")
	}
}

func TestTheNightKeepsToItsLimitAndOneToAFolder(t *testing.T) {
	r := newQueueRig(t, protocol.Autopilot{Overnight: true})
	r.add(t, "api", "First in api")
	r.add(t, "api", "Second in api")
	r.add(t, "site", "In site")
	r.add(t, "docs", "In docs")
	r.tick()
	if len(r.starts) != defaultNightAgents || !strings.Contains(r.starts[0].Text, "First in api") || !strings.Contains(r.starts[1].Text, "In site") {
		t.Fatalf("%+v", r.starts)
	}
}

func TestWhenOneFinishesTheNextStarts(t *testing.T) {
	r := newQueueRig(t, protocol.Autopilot{Overnight: true, NightAgents: 1})
	r.add(t, "api", "First")
	r.add(t, "site", "Second")
	r.tick()
	r.h.agents.setSessions([]protocol.Session{{ID: "s1", State: protocol.StateIdle}})
	r.h.m.queueTurnEnded("s1")
	r.tick()
	if len(r.starts) != 1 {
		t.Fatal("didn't wait for it to settle")
	}
	r.now = r.now.Add(queueSettle + time.Second)
	r.tick()
	if len(r.starts) != 2 {
		t.Fatalf("%+v", r.h.m.queue.all())
	}
}

func TestTheMorningGetsOneReport(t *testing.T) {
	r := newQueueRig(t, protocol.Autopilot{Overnight: true})
	r.add(t, "api", "Throttle the webhooks")
	r.tick()
	r.h.agents.setSessions([]protocol.Session{{ID: "s1", State: protocol.StateIdle}})
	r.h.m.queueTurnEnded("s1")
	r.h.m.verifies.put(protocol.Verification{SessionID: "s1", State: protocol.VerifyVerified, Summary: "Limited to 120 a minute."})
	r.now = at(23, 45)
	r.tick()
	r.now = at(8, 0)
	r.tick()
	r.tick()
	var reports []string
	for _, entry := range r.h.entries() {
		if entry.Role == "brief" {
			reports = append(reports, entry.Text)
		}
	}
	if len(reports) != 1 || !strings.Contains(reports[0], "1 of 1 finished") || !strings.Contains(reports[0], "Limited to 120") {
		t.Fatalf("%q", reports)
	}
	if got := r.h.m.queue.all(); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestTheNightCanCrossMidnightOrNot(t *testing.T) {
	night := &protocol.Autopilot{NightStart: "22:00", NightEnd: "06:00"}
	for _, c := range []struct {
		hour int
		want bool
	}{{23, true}, {2, true}, {6, false}, {12, false}, {22, true}} {
		if got := nightWindow(night, at(c.hour, 0)); got != c.want {
			t.Errorf("%d:00 = %v", c.hour, got)
		}
	}
	day := &protocol.Autopilot{NightStart: "09:00", NightEnd: "17:00"}
	if !nightWindow(day, at(12, 0)) || nightWindow(day, at(20, 0)) {
		t.Error("a window within one day")
	}
}

func TestBadWorkAndTimesAreRefused(t *testing.T) {
	r := newQueueRig(t, protocol.Autopilot{Overnight: true})
	if _, err := r.h.m.queueWork("gpt", filepath.Join(r.home, "api"), "x"); err == nil {
		t.Error("unknown agent")
	}
	if _, err := r.h.m.queueWork("claude", "/etc", "x"); err == nil {
		t.Error("outside home")
	}
	if _, err := r.h.m.queueWork("claude", filepath.Join(r.home, "api"), " "); err == nil {
		t.Error("no task")
	}
	if validateAutopilot(&protocol.Autopilot{NightStart: "25:00"}) == nil || validateAutopilot(&protocol.Autopilot{NightAgents: 99}) == nil {
		t.Error("bad night settings passed")
	}
}

func TestOnlyWaitingWorkCanBeRemoved(t *testing.T) {
	r := newQueueRig(t, protocol.Autopilot{Overnight: true})
	item := r.add(t, "api", "Do it")
	r.tick()
	event := r.h.m.handleQueueRequest(protocol.Request{Type: protocol.ReqManagerQueueRemove, QueueID: item.ID})
	if event.Type != protocol.EvtError {
		t.Fatal("removed work under way")
	}
}
