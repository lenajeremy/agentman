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

type missionRig struct {
	h      *harness
	now    time.Time
	folder string
	starts []protocol.Request
}

func newMissionRig(t *testing.T) *missionRig {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	folder := filepath.Join(home, "api")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, &scriptedModel{}, true)
	r := &missionRig{h: h, now: time.UnixMilli(10_000_000), folder: folder}
	h.m.now = func() time.Time { return r.now }
	h.agents.setSessions(nil)
	h.agents.answer = func(req protocol.Request) protocol.Event {
		switch req.Type {
		case protocol.ReqStartSession:
			r.starts = append(r.starts, req)
			return protocol.Event{SessionID: "s" + string(rune('0'+len(r.starts)))}
		case protocol.ReqCreateWorktree:
			return protocol.Event{Worktree: &protocol.Worktree{Path: filepath.Join(home, "wt", req.Text), Name: req.Text}}
		}
		return protocol.Event{}
	}
	return r
}

func (r *missionRig) task(id, title string, after ...any) map[string]any {
	return map[string]any{"id": id, "title": title, "agent": "claude", "folder": r.folder, "task": "Do " + title, "depends_on": after}
}

func (r *missionRig) propose(t *testing.T, tasks ...any) protocol.ManagerCard {
	t.Helper()
	result := runPlanTeam(context.Background(), r.h.m, map[string]any{"goal": "Add Apple Pay", "tasks": tasks})
	if result.isError {
		t.Fatal(result.text)
	}
	cards := r.h.m.conversation.openCards()
	if len(cards) != 1 {
		t.Fatalf("%+v", cards)
	}
	return *cards[0].Card
}

func (r *missionRig) answer(t *testing.T, card protocol.ManagerCard, option string) {
	t.Helper()
	if event := r.h.m.replyToCard(context.Background(), protocol.Request{CardID: card.ID, OptionKey: option}); event.Type == protocol.EvtError {
		t.Fatal(event.Error)
	}
	time.Sleep(20 * time.Millisecond)
	r.h.m.advanceMissions(context.Background())
}

func (r *missionRig) idle(id string) protocol.Session {
	return protocol.Session{ID: id, Name: id, Cwd: r.folder, State: protocol.StateIdle}
}

func TestAPlanStartsNothingUntilApproved(t *testing.T) {
	r := newMissionRig(t)
	card := r.propose(t, r.task("a", "Backend"), r.task("b", "Frontend", "a"))
	if len(r.starts) != 0 || !strings.Contains(card.Detail, "Frontend") || !strings.Contains(card.Detail, "after Backend") {
		t.Fatalf("%d started; %s", len(r.starts), card.Detail)
	}
	r.answer(t, card, "no")
	if len(r.starts) != 0 || r.h.m.missions.all()[0].State != protocol.MissionDismissed {
		t.Fatalf("%+v", r.h.m.missions.all())
	}
}

func TestATeamStartsInOrderAndFinishes(t *testing.T) {
	r := newMissionRig(t)
	r.answer(t, r.propose(t, r.task("a", "Backend"), r.task("b", "Frontend", "a")), "start")
	if len(r.starts) != 1 || !strings.Contains(r.starts[0].Text, "Add Apple Pay") || !strings.Contains(r.starts[0].Text, "Do Backend") {
		t.Fatalf("%+v", r.starts)
	}
	r.h.agents.setSessions([]protocol.Session{r.idle("s1")})
	r.h.m.advanceMissions(context.Background())
	if len(r.starts) != 1 {
		t.Fatal("started the next before the first took a turn")
	}
	r.h.m.missionTurnEnded("s1")
	r.h.m.advanceMissions(context.Background())
	if len(r.starts) != 1 {
		t.Fatal("started before the turn had settled")
	}
	r.now = r.now.Add(missionSettle + time.Second)
	r.h.m.advanceMissions(context.Background())
	if len(r.starts) != 2 {
		t.Fatalf("the next never started: %+v", r.h.m.missions.all())
	}
	r.h.agents.setSessions([]protocol.Session{r.idle("s1"), r.idle("s2")})
	r.h.m.missionTurnEnded("s2")
	r.now = r.now.Add(missionSettle + time.Second)
	r.h.m.advanceMissions(context.Background())
	if got := r.h.m.missions.all()[0]; got.State != protocol.MissionDone {
		t.Fatalf("%+v", got)
	}
	if told := r.h.told(); len(told) == 0 || told[len(told)-1] != "brief" {
		t.Fatalf("told: %v", told)
	}
}

func TestFailedChecksHoldTheNextAgentBack(t *testing.T) {
	r := newMissionRig(t)
	r.answer(t, r.propose(t, r.task("a", "Backend"), r.task("b", "Frontend", "a")), "start")
	r.h.agents.setSessions([]protocol.Session{r.idle("s1")})
	r.h.m.missionTurnEnded("s1")
	r.h.m.verifies.put(protocol.Verification{SessionID: "s1", State: protocol.VerifyFailed, SentBack: true})
	r.now = r.now.Add(missionSettle + time.Second)
	r.h.m.advanceMissions(context.Background())
	if len(r.starts) != 1 {
		t.Fatal("started after a failure")
	}
	r.h.m.verifies.put(protocol.Verification{SessionID: "s1", State: protocol.VerifyVerified})
	r.h.m.advanceMissions(context.Background())
	if len(r.starts) != 2 {
		t.Fatal("never started once it held up")
	}
}

func TestAPartThatFailsEndsWhatDependsOnIt(t *testing.T) {
	r := newMissionRig(t)
	r.answer(t, r.propose(t, r.task("a", "Backend"), r.task("b", "Frontend", "a")), "start")
	r.now = r.now.Add(5 * time.Minute)
	r.h.m.advanceMissions(context.Background())
	got := r.h.m.missions.all()[0]
	if got.State != protocol.MissionDone || got.Tasks[0].State != protocol.TaskFailed || got.Tasks[1].State != protocol.TaskPlanned || len(r.starts) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestAWorktreeIsMadeForAPartThatAsks(t *testing.T) {
	r := newMissionRig(t)
	withTree := r.task("a", "Backend")
	withTree["worktree"] = "backend"
	r.answer(t, r.propose(t, withTree, r.task("b", "Frontend")), "start")
	if len(r.starts) != 2 || !strings.HasSuffix(r.starts[0].Path, "wt/backend") || r.starts[1].Path != "api" {
		t.Fatalf("%+v", r.starts)
	}
}

func TestBadPlansAreRefused(t *testing.T) {
	r := newMissionRig(t)
	cases := map[string][]any{
		"one task":     {r.task("a", "Only")},
		"unknown dep":  {r.task("a", "A", "z"), r.task("b", "B")},
		"a circle":     {r.task("a", "A", "b"), r.task("b", "B", "a")},
		"duplicate id": {r.task("a", "A"), r.task("a", "B")},
	}
	unknown := r.task("b", "B")
	unknown["agent"] = "gpt"
	cases["unknown agent"] = []any{r.task("a", "A"), unknown}
	outside := r.task("b", "B")
	outside["folder"] = "/etc"
	cases["outside home"] = []any{r.task("a", "A"), outside}
	for name, tasks := range cases {
		if result := runPlanTeam(context.Background(), r.h.m, map[string]any{"goal": "G", "tasks": tasks}); !result.isError {
			t.Errorf("%s was accepted", name)
		}
	}
	if len(r.h.m.missions.all()) != 0 {
		t.Fatal("a refused plan was kept")
	}
}

func TestAChainBehindAFailureEndsTheTeam(t *testing.T) {
	r := newMissionRig(t)
	r.answer(t, r.propose(t, r.task("a", "A"), r.task("b", "B", "a"), r.task("c", "C", "b")), "start")
	r.now = r.now.Add(5 * time.Minute)
	r.h.m.advanceMissions(context.Background())
	if got := r.h.m.missions.all()[0]; got.State != protocol.MissionDone {
		t.Fatalf("%+v", got)
	}
}

func TestTestsThatStayFailingEndThatPart(t *testing.T) {
	r := newMissionRig(t)
	r.answer(t, r.propose(t, r.task("a", "A"), r.task("b", "B", "a")), "start")
	r.h.agents.setSessions([]protocol.Session{r.idle("s1")})
	r.h.m.missionTurnEnded("s1")
	r.h.m.verifies.put(protocol.Verification{SessionID: "s1", State: protocol.VerifyFailed, SentBack: true})
	r.now = r.now.Add(missionSettle + time.Second)
	r.h.m.advanceMissions(context.Background())
	if got := r.h.m.missions.all()[0]; got.State != protocol.MissionRunning {
		t.Fatalf("sent back to fix, so still going: %+v", got)
	}
	r.h.m.verifies.put(protocol.Verification{SessionID: "s1", State: protocol.VerifyFailed})
	r.h.m.advanceMissions(context.Background())
	got := r.h.m.missions.all()[0]
	if got.State != protocol.MissionDone || got.Tasks[0].State != protocol.TaskFailed || len(r.starts) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestATurnThatEndedWhileTheDaemonWasDownIsNoticed(t *testing.T) {
	r := newMissionRig(t)
	r.answer(t, r.propose(t, r.task("a", "A"), r.task("b", "B", "a")), "start")
	r.h.agents.setSessions([]protocol.Session{r.idle("s1")})
	worked := r.now.UnixMilli() + 1000
	answer := r.h.agents.answer
	r.h.agents.answer = func(req protocol.Request) protocol.Event {
		if req.Type == protocol.ReqFetchMessages {
			return protocol.Event{Page: &protocol.Page{Messages: []protocol.Message{{ID: "1", Role: protocol.RoleAssistant, Ts: worked, Text: "Done"}}}}
		}
		return answer(req)
	}
	r.now = r.now.Add(5 * time.Minute)
	r.h.m.advanceMissions(context.Background())
	r.now = r.now.Add(missionSettle + time.Second)
	r.h.m.advanceMissions(context.Background())
	if len(r.starts) != 2 {
		t.Fatalf("%+v", r.h.m.missions.all())
	}
}
