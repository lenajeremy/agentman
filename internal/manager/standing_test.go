package manager

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// The checks on a need's own words, whichever kind the model calls it. The
// shapes are real cards', with their names changed.
func TestAStandingAnswerCoversOnlyTheOneThing(t *testing.T) {
	cases := []struct {
		need          string
		commit, build bool
	}{
		{"Nothing from today's work is committed. Tell the agent when to commit it.", true, false},
		{"Tell it when to commit the calendar colour changes.", true, false},
		{"Say whether to commit the fix.", true, false},
		{"Say whether to update the installed Mac app with this pass.", false, true},
		{`Say "install" if you want the new build put on your phone.`, false, true},

		{"Say whether to commit and push the QR fix.", false, false},
		{"Decide: commit only, or cut a release so downloads get the new command.", false, false},
		{"Decide whether to commit and run the deploy.", false, false},
		{"Review the new section at the local page and say whether to commit.", false, false},
		{"Try the new build on your phone.", false, false},
		{"Install Tailscale on your phone and log in with the same account.", false, false},
		{"Commit your changes and install the updated app on my phone.", false, false},
		{"Say deploy to put it live.", false, false},
		{"Merge PR 174 yourself.", false, false},
		{"Say whether to release this on TestFlight for your phone.", false, false},
		{"Pick an app name.", false, false},
		{"Approve the plan to refresh the sign-in.", false, false},
		{"Install it on the staging server.", false, false},
		{"", false, false},
		{"Say whether to commit. " + strings.Repeat("It is ready. ", 30), false, false},
	}
	for _, c := range cases {
		if got := standingCovers(c.need, standingCommit); got != c.commit {
			t.Errorf("as a commit: %q = %v", c.need, got)
		}
		if got := standingCovers(c.need, standingInstall); got != c.build {
			t.Errorf("as an install: %q = %v", c.need, got)
		}
	}
	if standingCovers("Say whether to commit the fix.", "deploy") {
		t.Error("answered a kind there is no standing answer for")
	}
}

type standingRig struct {
	h        *harness
	model    *scriptedModel
	messages []protocol.Message
}

func standingReplyJSON(need, standing string) string {
	raw, _ := json.Marshal(map[string]any{
		"summary": "The fix is ready.", "goal": "Fix it", "done": []string{}, "now": "", "blocked": "",
		"needsYou": need, "heldBack": "", "standing": standing, "files": []string{}, "notable": false,
	})
	return string(raw)
}

func newStandingRig(t *testing.T, autopilot *protocol.Autopilot, replies ...string) *standingRig {
	t.Helper()
	scripted := make([]string, 0, len(replies))
	for _, reply := range replies {
		scripted = append(scripted, textReply(reply))
	}
	r := &standingRig{model: &scriptedModel{replies: scripted}, messages: []protocol.Message{
		{ID: "1", Role: protocol.RoleUser, Ts: 1, Text: "fix the header"},
		{ID: "2", Role: protocol.RoleAssistant, Ts: 2, Text: "Done. Want me to commit it?"},
	}}
	r.h = newHarness(t, r.model, true)
	r.h.agents.setSessions([]protocol.Session{{ID: "claude:a", Kind: "claude", Name: "site", Cwd: "/w/site",
		State: protocol.StateIdle, Inject: protocol.InjectTmux}})
	r.h.agents.answer = func(req protocol.Request) protocol.Event {
		if req.Type == protocol.ReqFetchMessages {
			return protocol.Event{Type: protocol.EvtPage, Page: &protocol.Page{SessionID: req.SessionID, Messages: append([]protocol.Message(nil), r.messages...)}}
		}
		return protocol.Event{Status: protocol.StatusDelivered}
	}
	r.h.m.mu.Lock()
	r.h.m.settings.Autopilot = autopilot
	r.h.m.mu.Unlock()
	return r
}

func (r *standingRig) brief() { r.h.m.writeBrief(context.Background(), "claude:a") }

func (r *standingRig) sent() []string {
	var out []string
	for _, req := range r.h.agents.sent(protocol.ReqSendMessage) {
		out = append(out, req.Text)
	}
	return out
}

func (r *standingRig) needCards() int {
	count := 0
	for _, entry := range r.h.cards() {
		if entry.Card.Kind == protocol.CardNeedsYou && entry.Card.State == protocol.CardOpen {
			count++
		}
	}
	return count
}

func TestAStandingAnswerIsAFixedLineAndNoCard(t *testing.T) {
	r := newStandingRig(t, &protocol.Autopilot{ReplyCommit: true}, standingReplyJSON("Say whether to commit the fix.", "commit"))
	r.brief()

	sent := r.sent()
	if len(sent) != 1 || sent[0] != standingOpening+"Yes, commit it. Don't push." {
		t.Fatalf("sent %q", sent)
	}
	if r.needCards() != 0 {
		t.Fatalf("a card went up for what was answered: %+v", r.h.cards())
	}
	if brief, _ := r.h.m.briefs.get("claude:a"); brief.NeedsYou != "" {
		t.Fatalf("the agent still reads as needing the user: %q", brief.NeedsYou)
	}
	said := false
	for _, entry := range r.h.entries() {
		said = said || entry.Tool == "approve" && strings.Contains(entry.Text, "Told site")
	}
	if !said {
		t.Fatalf("nothing says it was answered: %+v", r.h.entries())
	}
	if !isOwnMessage(sent[0]) {
		t.Fatal("what was sent would count as a new request from the user")
	}
}

func TestAStandingAnswerWaitsForTheUserWhenItShould(t *testing.T) {
	on := &protocol.Autopilot{ReplyCommit: true, ReplyInstall: true}
	commit := standingReplyJSON("Say whether to commit the fix.", "commit")
	for _, c := range []struct {
		name      string
		autopilot *protocol.Autopilot
		reply     string
		prepare   func(*standingRig)
	}{
		{name: "never turned on", reply: commit},
		{name: "this one off", autopilot: &protocol.Autopilot{ReplyInstall: true}, reply: commit},
		{name: "the model calls it nothing", autopilot: on, reply: standingReplyJSON("Say whether to commit the fix.", "")},
		// The model has been talked round; the need's own words stop it.
		{name: "the model calls a deploy a commit", autopilot: on, reply: standingReplyJSON("Say whether to commit and deploy.", "commit")},
		{name: "a kind that doesn't exist", autopilot: on, reply: standingReplyJSON("Say whether to commit the fix.", "push")},
		{name: "the folder is on Watch", autopilot: on, reply: commit, prepare: func(r *standingRig) {
			r.h.m.mu.Lock()
			r.h.m.settings.Trust = []protocol.FolderTrust{{Folder: "/w/site", Level: protocol.TrustWatch}}
			r.h.m.mu.Unlock()
		}},
		{name: "the agent has gone back to work", autopilot: on, reply: commit, prepare: func(r *standingRig) {
			r.h.agents.setSessions([]protocol.Session{{ID: "claude:a", Kind: "claude", Name: "site", Cwd: "/w/site",
				State: protocol.StateBusy, Inject: protocol.InjectTmux}})
		}},
		{name: "the agent can't be written to", autopilot: on, reply: commit, prepare: func(r *standingRig) {
			r.h.agents.setSessions([]protocol.Session{{ID: "claude:a", Kind: "claude", Name: "site", Cwd: "/w/site",
				State: protocol.StateIdle, Inject: protocol.InjectNone}})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newStandingRig(t, c.autopilot, c.reply)
			if c.prepare != nil {
				c.prepare(r)
			}
			r.brief()
			if sent := r.sent(); len(sent) != 0 {
				t.Fatalf("sent %q", sent)
			}
			if r.needCards() != 1 {
				t.Fatalf("want the need on a card, got %+v", r.h.cards())
			}
		})
	}
}

// With the user looking at the agent nothing is said over them, and the
// need is held for when they leave, as it always was.
func TestAStandingAnswerIsNotGivenWhileTheUserIsLooking(t *testing.T) {
	r := newStandingRig(t, &protocol.Autopilot{ReplyCommit: true}, standingReplyJSON("Say whether to commit the fix.", "commit"))
	r.h.m.setViewing(context.Background(), "mac", []string{"claude:a"})
	r.brief()
	if sent := r.sent(); len(sent) != 0 {
		t.Fatalf("sent %q while the user was looking", sent)
	}
}

// One request of the user's buys a commit and an install, not a conversation:
// what Agentman sends doesn't count as the user asking again.
func TestStandingAnswersRunOut(t *testing.T) {
	commit := standingReplyJSON("Say whether to commit the fix.", "commit")
	r := newStandingRig(t, &protocol.Autopilot{ReplyCommit: true}, commit, commit, commit)
	next := int64(3)
	for range 3 {
		r.brief()
		sent := r.sent()
		r.messages = append(r.messages,
			protocol.Message{ID: "own", Role: protocol.RoleUser, Ts: next, Text: sent[len(sent)-1]},
			protocol.Message{ID: "more" + string(rune('a'+next)), Role: protocol.RoleAssistant, Ts: next + 1, Text: "Shall I commit that too?"})
		next += 2
	}
	if got := len(r.sent()); got != maxStandingAnswers {
		t.Fatalf("sent %d answers, want %d", got, maxStandingAnswers)
	}
	if r.needCards() != 1 {
		t.Fatalf("when they run out the need should come to the user: %+v", r.h.cards())
	}
}
