package manager

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

type shipRig struct {
	h   *harness
	git map[string]*protocol.GitState
	bad map[protocol.RequestType]string
}

func newShipRig(t *testing.T, ids ...string) *shipRig {
	t.Helper()
	h := newHarness(t, &scriptedModel{}, true)
	r := &shipRig{h: h, git: map[string]*protocol.GitState{}, bad: map[protocol.RequestType]string{}}
	var sessions []protocol.Session
	for _, id := range ids {
		sessions = append(sessions, protocol.Session{ID: id, Name: "Agent " + id, Cwd: "/w/" + id, State: protocol.StateIdle})
		r.git[id] = &protocol.GitState{Branch: "feat/" + id, DefaultBranch: "main", Remote: "origin", Changes: 2, AheadOfDefault: 1}
	}
	h.agents.setSessions(sessions)
	h.agents.answer = func(req protocol.Request) protocol.Event {
		if message, failing := r.bad[req.Type]; failing {
			return protocol.Event{Type: protocol.EvtError, Error: message}
		}
		switch req.Type {
		case protocol.ReqGitStatus:
			return protocol.Event{Git: r.git[req.SessionID]}
		case protocol.ReqListChanges:
			return protocol.Event{Workspace: &protocol.WorkspaceResult{Changes: []protocol.WorkspaceChange{{Path: "a.go"}, {Path: "b.go"}}, Hidden: 1}}
		case protocol.ReqGitCreatePR:
			return protocol.Event{Git: &protocol.GitState{URL: "https://github.com/x/pull/1"}}
		}
		return protocol.Event{}
	}
	return r
}

func (r *shipRig) ship(t *testing.T, ids ...string) protocol.ShipRun {
	t.Helper()
	if event := r.h.m.startShip(ids); event.Type == protocol.EvtError {
		t.Fatal(event.Error)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if run := r.h.m.shipping.current(); run != nil && r.h.m.shipping.change(func(*protocol.ShipRun) {}).State == protocol.ShipDone {
			return r.h.m.shipping.change(func(*protocol.ShipRun) {})
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("never finished")
	return protocol.ShipRun{}
}

func TestShippingCommitsPushesAndOpensAPullRequest(t *testing.T) {
	r := newShipRig(t, "a", "b")
	run := r.ship(t, "a", "b")
	for _, item := range run.Items {
		if item.State != protocol.ShipShipped || !strings.Contains(item.URL, "/pull/") {
			t.Fatalf("%+v", item)
		}
	}
	commits := r.h.agents.sent(protocol.ReqGitCommit)
	if len(commits) != 2 || len(commits[0].Paths) != 2 || commits[0].Text != "" {
		t.Fatalf("%+v", commits)
	}
	if len(r.h.agents.sent(protocol.ReqGitPush)) != 2 || len(r.h.agents.sent(protocol.ReqGitCreatePR)) != 2 {
		t.Fatal("skipped a step")
	}
}

func TestADefaultBranchIsNeverPushed(t *testing.T) {
	r := newShipRig(t, "a")
	r.git["a"].Branch = "main"
	run := r.ship(t, "a")
	if run.Items[0].State != protocol.ShipSkipped || len(r.h.agents.sent(protocol.ReqGitPush)) != 0 || len(r.h.agents.sent(protocol.ReqGitCommit)) != 0 {
		t.Fatalf("%+v", run.Items[0])
	}
}

func TestFailingTestsAndBusyAgentsAreLeftAlone(t *testing.T) {
	r := newShipRig(t, "a", "b")
	r.h.m.verifies.put(protocol.Verification{SessionID: "a", State: protocol.VerifyFailed})
	sessions := r.h.agents.Sessions()
	sessions[1].State = protocol.StateBusy
	r.h.agents.setSessions(sessions)
	run := r.ship(t, "a", "b")
	if run.Items[0].State != protocol.ShipSkipped || run.Items[1].State != protocol.ShipSkipped || len(r.h.agents.sent(protocol.ReqGitPush)) != 0 {
		t.Fatalf("%+v", run.Items)
	}
}

func TestAFailedPushStopsThatAgentOnly(t *testing.T) {
	r := newShipRig(t, "a", "b")
	r.git["a"].Remote = "origin"
	r.bad[protocol.ReqGitPush] = "daemon: rejected"
	run := r.ship(t, "a", "b")
	if run.Items[0].State != protocol.ShipFailed || run.Items[1].State != protocol.ShipFailed || !strings.Contains(run.Items[0].Note, "push failed") {
		t.Fatalf("%+v", run.Items)
	}
	if len(r.h.agents.sent(protocol.ReqGitCreatePR)) != 0 {
		t.Fatal("opened a pull request after a failed push")
	}
}

func TestOneRunAtATime(t *testing.T) {
	r := newShipRig(t, "a")
	r.h.m.shipping.begin(protocol.ShipRun{State: protocol.ShipRunning})
	if event := r.h.m.startShip([]string{"a"}); event.Type != protocol.EvtError {
		t.Fatalf("%+v", event)
	}
	_ = context.Background()
}
