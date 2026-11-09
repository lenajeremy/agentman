package manager

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

type overlapRig struct {
	h     *harness
	now   time.Time
	byID  map[string][]protocol.Message
	repos map[string]string
}

func newOverlapRig(t *testing.T, trust string, sessions ...protocol.Session) *overlapRig {
	t.Helper()
	h := newHarness(t, &scriptedModel{}, true)
	r := &overlapRig{h: h, now: time.UnixMilli(10_000_000), byID: map[string][]protocol.Message{}, repos: map[string]string{}}
	for i := range sessions {
		sessions[i].State, sessions[i].LastActivityAt, sessions[i].Inject = protocol.StateBusy, r.now.UnixMilli(), protocol.InjectTmux
	}
	h.agents.setSessions(sessions)
	h.m.now = func() time.Time { return r.now }
	h.m.runCheck = func(_ context.Context, dir, _ string, _ ...string) (string, error) { return r.repos[dir], nil }
	h.agents.answer = func(req protocol.Request) protocol.Event {
		if req.Type == protocol.ReqFetchMessages {
			return protocol.Event{Page: &protocol.Page{Messages: r.byID[req.SessionID]}}
		}
		return protocol.Event{Status: protocol.StatusDelivered}
	}
	h.m.mu.Lock()
	h.m.settings.Autopilot = &protocol.Autopilot{Overlap: true}
	h.m.settings.Trust = []protocol.FolderTrust{{Folder: "/w", Level: trust}}
	h.m.mu.Unlock()
	return r
}

func step(id, name, path string, ts int64) protocol.Message {
	return protocol.Message{ID: id, Role: protocol.RoleTool, Ts: ts, Tool: &protocol.Tool{Name: name, Summary: path, Status: protocol.ToolOK}}
}

func twoAgents() (protocol.Session, protocol.Session) {
	return protocol.Session{ID: "a", Name: "Limits", Cwd: "/w/api"}, protocol.Session{ID: "b", Name: "Retries", Cwd: "/w/api"}
}

func TestTwoAgentsChangingOneFileAreRaisedOnce(t *testing.T) {
	a, b := twoAgents()
	r := newOverlapRig(t, protocol.TrustSuggest, a, b)
	r.byID["a"] = []protocol.Message{step("1", "Edit", "/w/api/webhooks/limit.go", 1)}
	r.byID["b"] = []protocol.Message{step("2", "Write", "webhooks/limit.go", 2)}
	r.h.m.checkStuck(context.Background())
	r.h.m.checkStuck(context.Background())
	got := r.h.m.conversation.openCards()
	if len(got) != 1 || !strings.Contains(got[0].Card.Title, "webhooks/limit.go") {
		t.Fatalf("%+v", got)
	}
	if len(r.h.agents.sent(protocol.ReqSendMessage)) != 0 {
		t.Fatal("told them unasked")
	}
	if event := r.h.m.replyToCard(context.Background(), protocol.Request{CardID: got[0].Card.ID, OptionKey: "tell"}); event.Type == protocol.EvtError {
		t.Fatal(event.Error)
	}
	sent := r.h.agents.sent(protocol.ReqSendMessage)
	if len(sent) != 2 || !strings.Contains(sent[0].Text, "Retries") || !strings.Contains(sent[1].Text, "Limits") {
		t.Fatalf("%+v", sent)
	}
}

func TestOnHandleBothAreToldAndNoCardGoesUp(t *testing.T) {
	a, b := twoAgents()
	r := newOverlapRig(t, protocol.TrustHandle, a, b)
	r.byID["a"] = []protocol.Message{step("1", "Edit", "x.go", 1)}
	r.byID["b"] = []protocol.Message{step("2", "Edit", "x.go", 2)}
	r.h.m.checkStuck(context.Background())
	if len(r.h.agents.sent(protocol.ReqSendMessage)) != 2 || len(r.h.m.conversation.openCards()) != 0 {
		t.Fatal("expected both told")
	}
}

func TestAnAgentThatReadAFileLaterChangedIsOffered(t *testing.T) {
	a, b := twoAgents()
	r := newOverlapRig(t, protocol.TrustSuggest, a, b)
	r.byID["a"] = []protocol.Message{step("1", "Edit", "schema.sql", 20)}
	r.byID["b"] = []protocol.Message{step("2", "Read", "schema.sql", 10)}
	r.h.m.checkStuck(context.Background())
	got := r.h.m.conversation.openCards()
	if len(got) != 1 || got[0].Card.Offer["forA"] != "" || !strings.Contains(got[0].Card.Offer["forB"], "Limits just changed") {
		t.Fatalf("%+v", got)
	}
}

func TestReadingAfterTheChangeIsFine(t *testing.T) {
	a, b := twoAgents()
	r := newOverlapRig(t, protocol.TrustSuggest, a, b)
	r.byID["a"] = []protocol.Message{step("1", "Edit", "schema.sql", 10)}
	r.byID["b"] = []protocol.Message{step("2", "Read", "schema.sql", 20)}
	r.h.m.checkStuck(context.Background())
	if got := r.h.m.conversation.openCards(); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestWorktreesOfOneRepoCountButOtherReposDont(t *testing.T) {
	a := protocol.Session{ID: "a", Name: "One", Cwd: "/w/one"}
	b := protocol.Session{ID: "b", Name: "Two", Cwd: "/w/two"}
	r := newOverlapRig(t, protocol.TrustSuggest, a, b)
	r.byID["a"] = []protocol.Message{step("1", "Edit", "x.go", 1)}
	r.byID["b"] = []protocol.Message{step("2", "Edit", "x.go", 2)}
	r.repos["/w/one"], r.repos["/w/two"] = "/w/.git-one", "/w/.git-two"
	r.h.m.checkStuck(context.Background())
	if got := r.h.m.conversation.openCards(); len(got) != 0 {
		t.Fatalf("different repositories: %+v", got)
	}
	r.repos["/w/two"] = "/w/.git-one"
	r.h.m.checkStuck(context.Background())
	if got := r.h.m.conversation.openCards(); len(got) != 1 {
		t.Fatalf("worktrees of one repository: %+v", got)
	}
}

func TestLongIdleAgentsAndOffSwitchAreLeftAlone(t *testing.T) {
	a, b := twoAgents()
	r := newOverlapRig(t, protocol.TrustSuggest, a, b)
	r.byID["a"] = []protocol.Message{step("1", "Edit", "x.go", 1)}
	r.byID["b"] = []protocol.Message{step("2", "Edit", "x.go", 2)}
	r.now = r.now.Add(time.Hour)
	r.h.m.checkStuck(context.Background())
	if got := r.h.m.conversation.openCards(); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	r.now = r.now.Add(-time.Hour)
	r.h.m.mu.Lock()
	r.h.m.settings.Autopilot = &protocol.Autopilot{}
	r.h.m.mu.Unlock()
	r.h.m.checkStuck(context.Background())
	if got := r.h.m.conversation.openCards(); len(got) != 0 {
		t.Fatalf("off, but: %+v", got)
	}
}
