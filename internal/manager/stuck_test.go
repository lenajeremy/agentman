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

type stuckRig struct {
	h        *harness
	now      time.Time
	messages []protocol.Message
	session  protocol.Session
}

func newStuckRig(t *testing.T, trust string) *stuckRig {
	t.Helper()
	h := newHarness(t, &scriptedModel{}, true)
	r := &stuckRig{h: h, now: time.UnixMilli(10_000_000)}
	r.session = protocol.Session{ID: "a", Kind: protocol.KindClaude, Name: "Throttle", Cwd: "/w/api", State: protocol.StateBusy,
		Inject: protocol.InjectTmux, LastActivityAt: r.now.UnixMilli()}
	h.m.now = func() time.Time { return r.now }
	h.agents.answer = func(req protocol.Request) protocol.Event {
		switch req.Type {
		case protocol.ReqFetchMessages:
			return protocol.Event{Page: &protocol.Page{Messages: r.messages}}
		case protocol.ReqStartSession:
			return protocol.Event{SessionID: "new"}
		}
		return protocol.Event{Status: protocol.StatusDelivered}
	}
	h.m.mu.Lock()
	h.m.settings.Autopilot = &protocol.Autopilot{Unstick: true}
	h.m.settings.Trust = []protocol.FolderTrust{{Folder: "/w", Level: trust}}
	h.m.mu.Unlock()
	return r
}

func (r *stuckRig) sweep() {
	r.h.agents.setSessions([]protocol.Session{r.session})
	r.h.m.checkStuck(context.Background())
}

func failedSteps(n int) []protocol.Message {
	messages := []protocol.Message{{ID: "u", Role: protocol.RoleUser, Text: "Go"}}
	for i := 0; i < n; i++ {
		messages = append(messages, protocol.Message{ID: "t" + string(rune('a'+i)), Role: protocol.RoleTool,
			Tool: &protocol.Tool{Name: "Bash", Summary: "go test", Status: protocol.ToolError}})
	}
	return messages
}

func cards(h *harness) []protocol.ManagerEntry { return h.m.conversation.openCards() }

func TestNothingIsWatchedUntilTurnedOn(t *testing.T) {
	r := newStuckRig(t, protocol.TrustSuggest)
	r.h.m.mu.Lock()
	r.h.m.settings.Autopilot = nil
	r.h.m.mu.Unlock()
	r.messages = failedSteps(6)
	r.sweep()
	if len(cards(r.h)) != 0 || len(r.h.agents.sent(protocol.ReqFetchMessages)) != 0 {
		t.Fatal("watched while off")
	}
}

func TestRepeatedFailureIsOfferedOnSuggestAndOnlyOnce(t *testing.T) {
	r := newStuckRig(t, protocol.TrustSuggest)
	r.messages = failedSteps(failingRun)
	r.sweep()
	r.sweep()
	got := cards(r.h)
	if len(got) != 1 || got[0].Card.Offer["reason"] != stuckFailing || !strings.Contains(got[0].Card.Title, "failed 4 times") {
		t.Fatalf("%+v", got)
	}
	if len(r.h.agents.sent(protocol.ReqSendMessage)) != 0 {
		t.Fatal("told it without being trusted to")
	}
}

func TestOnHandleARepeatedFailureGetsOneNudge(t *testing.T) {
	r := newStuckRig(t, protocol.TrustHandle)
	r.messages = failedSteps(failingRun)
	r.sweep()
	r.sweep()
	sent := r.h.agents.sent(protocol.ReqSendMessage)
	if len(sent) != 1 || sent[0].Text != nudgeText || len(cards(r.h)) != 0 {
		t.Fatalf("sent %+v, cards %+v", sent, cards(r.h))
	}
}

func TestAWatchedFolderIsOnlyTold(t *testing.T) {
	r := newStuckRig(t, protocol.TrustWatch)
	r.messages = failedSteps(failingRun)
	r.sweep()
	if len(r.h.agents.sent(protocol.ReqSendMessage)) != 0 || len(cards(r.h)) != 1 {
		t.Fatal("acted in a watched folder")
	}
}

func TestAStepRepeatedIsStuckEvenWhenItSucceeds(t *testing.T) {
	r := newStuckRig(t, protocol.TrustSuggest)
	for i := 0; i < repeatRun; i++ {
		r.messages = append(r.messages, protocol.Message{ID: string(rune('a' + i)), Role: protocol.RoleTool, Tool: &protocol.Tool{Name: "Read", Summary: "a.go", Status: protocol.ToolOK}})
	}
	r.sweep()
	if got := cards(r.h); len(got) != 1 || !strings.Contains(got[0].Card.Title, "repeating") {
		t.Fatalf("%+v", got)
	}
}

func TestVariedWorkAndAFreshRequestAreNotStuck(t *testing.T) {
	r := newStuckRig(t, protocol.TrustSuggest)
	r.messages = append(failedSteps(failingRun), protocol.Message{ID: "z", Role: protocol.RoleUser, Text: "Try the other way"})
	r.sweep()
	if len(cards(r.h)) != 0 {
		t.Fatal("the user's new message should start over")
	}
}

func TestQuietCountsLongerWhileACommandRuns(t *testing.T) {
	r := newStuckRig(t, protocol.TrustSuggest)
	r.session.LastActivityAt = r.now.Add(-30 * time.Minute).UnixMilli()
	r.messages = []protocol.Message{{ID: "1", Role: protocol.RoleTool, Tool: &protocol.Tool{Name: "Bash", Summary: "make all", Status: protocol.ToolRunning}}}
	r.sweep()
	if len(cards(r.h)) != 0 {
		t.Fatal("a long build isn't quiet yet")
	}
	r.messages = nil
	r.sweep()
	if got := cards(r.h); len(got) != 1 || got[0].Card.Offer["reason"] != stuckQuiet {
		t.Fatalf("%+v", got)
	}
}

func TestACardGoesWhenTheAgentGetsGoing(t *testing.T) {
	r := newStuckRig(t, protocol.TrustSuggest)
	r.messages = failedSteps(failingRun)
	r.sweep()
	r.messages = append(r.messages, protocol.Message{ID: "ok", Role: protocol.RoleTool, Tool: &protocol.Tool{Name: "Edit", Summary: "x.go", Status: protocol.ToolOK}})
	r.sweep()
	if len(cards(r.h)) != 0 {
		t.Fatal("the card stayed")
	}
}

func TestNothingIsSaidOverAnAgentTheUserIsLookingAt(t *testing.T) {
	r := newStuckRig(t, protocol.TrustHandle)
	r.messages = failedSteps(failingRun)
	r.h.m.setViewing(context.Background(), "mac", []string{"a"})
	r.sweep()
	if len(r.h.agents.sent(protocol.ReqSendMessage)) != 0 || len(cards(r.h)) != 0 {
		t.Fatal("interrupted the user")
	}
}

func TestAFullContextOffersAFreshStart(t *testing.T) {
	r := newStuckRig(t, protocol.TrustHandle)
	r.session.ContextPercent = 95
	r.sweep()
	got := cards(r.h)
	if len(got) != 1 || got[0].Card.Offer["reason"] != stuckContext {
		t.Fatalf("%+v", got)
	}
	if len(r.h.agents.sent(protocol.ReqStartSession)) != 0 {
		t.Fatal("started one by itself")
	}
}

func TestALimitOffersAnotherAgentAndOnlyWhenItsTheLastWord(t *testing.T) {
	r := newStuckRig(t, protocol.TrustSuggest)
	r.session.State = protocol.StateIdle
	r.messages = []protocol.Message{{ID: "1", Role: protocol.RoleAssistant, Text: "I added rate limit handling to the client, with a long explanation of how it behaves when the API returns a 429 and what the retry policy looks like across the three call sites that use it. " + strings.Repeat("More. ", 30)}}
	r.sweep()
	if len(cards(r.h)) != 0 {
		t.Fatal("work about rate limits isn't a limit")
	}
	r.messages = []protocol.Message{{ID: "2", Role: protocol.RoleAssistant, Text: "You've hit your usage limit. It resets at 5pm."}}
	r.sweep()
	got := cards(r.h)
	if len(got) != 1 || got[0].Card.Offer["other"] != "codex" {
		t.Fatalf("%+v", got)
	}
}

func TestTakingUpTheOffers(t *testing.T) {
	r := newStuckRig(t, protocol.TrustSuggest)
	r.messages = failedSteps(failingRun)
	r.sweep()
	card := cards(r.h)[0].Card
	if event := r.h.m.replyToCard(context.Background(), protocol.Request{CardID: card.ID, OptionKey: "nudge"}); event.Type == protocol.EvtError {
		t.Fatal(event.Error)
	}
	if sent := r.h.agents.sent(protocol.ReqSendMessage); len(sent) != 1 || sent[0].Text != nudgeText {
		t.Fatalf("%+v", sent)
	}
}

func TestAFreshStartCarriesTheWorkOver(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	folder := filepath.Join(home, "api")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	r := newStuckRig(t, protocol.TrustSuggest)
	r.session.Cwd = folder
	r.session.ContextPercent = 95
	r.sweep()
	card := cards(r.h)[0].Card
	if event := r.h.m.replyToCard(context.Background(), protocol.Request{CardID: card.ID, OptionKey: "restart"}); event.Type == protocol.EvtError {
		t.Fatal(event.Error)
	}
	started := r.h.agents.sent(protocol.ReqStartSession)
	if len(started) != 1 || started[0].Kind != protocol.KindClaude || !strings.Contains(started[0].Text, "Carry on") {
		t.Fatalf("%+v", started)
	}
}

func TestLeavingAnOfferDoesNothing(t *testing.T) {
	r := newStuckRig(t, protocol.TrustSuggest)
	r.messages = failedSteps(failingRun)
	r.sweep()
	card := cards(r.h)[0].Card
	r.h.m.replyToCard(context.Background(), protocol.Request{CardID: card.ID, OptionKey: "leave"})
	if len(r.h.agents.sent(protocol.ReqSendMessage)) != 0 || len(cards(r.h)) != 0 {
		t.Fatal("something happened")
	}
}
