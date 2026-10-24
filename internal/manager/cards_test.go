package manager

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// permission is a Claude Code permission request to run command.
func permission(id, command string) *protocol.Question {
	return &protocol.Question{
		ID: id, Title: "Bash command", Detail: "Run it\n\n" + command, Prompt: "Do you want to proceed?",
		Options: []protocol.QuestionOption{
			{Key: "1", Label: "Yes"},
			{Key: "2", Label: "Yes, and don't ask again for this command"},
			{Key: "3", Label: "No, and tell Claude what to do differently", WithText: true},
		},
	}
}

// delivered is a daemon that takes every send and answer.
func delivered(req protocol.Request) protocol.Event {
	return protocol.Event{Type: protocol.EvtSendResult, SessionID: req.SessionID, Status: protocol.StatusDelivered}
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// asking is the api agent asking to run command.
func (h *harness) asking(id, command string) protocol.Session {
	sessions := h.agents.Sessions()
	sessions[1].State = protocol.StateWaitingInput
	sessions[1].Question = permission(id, command)
	h.agents.setSessions(sessions)
	return sessions[1]
}

// answered is the api agent with its question gone.
func (h *harness) answered() []protocol.Session {
	sessions := h.agents.Sessions()
	sessions[1].State = protocol.StateBusy
	sessions[1].Question = nil
	h.agents.setSessions(sessions)
	return sessions
}

func (h *harness) cards() []protocol.ManagerEntry {
	var out []protocol.ManagerEntry
	for _, entry := range h.entries() {
		if entry.Card != nil {
			out = append(out, entry)
		}
	}
	return out
}

// A request the user let Agentman handle is approved at once, with the
// plain yes, and the user is told in a line.
func TestALowRiskRequestIsApproved(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	h.agents.answer = delivered
	h.m.takeQuestion(context.Background(), h.asking("q1", "go test ./..."))

	answers := h.agents.sent(protocol.ReqAnswer)
	if len(answers) != 1 || answers[0].QuestionID != "q1" || answers[0].OptionKey != "1" || answers[0].SessionID != "codex:b" {
		t.Fatalf("answers = %+v", answers)
	}
	entries := h.entries()
	if len(entries) != 1 || entries[0].Role != "activity" || entries[0].Text != "Approved for api: run `go test ./...`" {
		t.Fatalf("entries = %+v", entries)
	}
	if len(h.cards()) != 0 {
		t.Fatal("an approved request also became a card")
	}
}

// With approving turned off, even a low-risk request waits for the user.
func TestApprovingCanBeTurnedOff(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	h.agents.answer = delivered
	settings := h.m.currentSettings()
	settings.AutoApprove = false
	if event := h.m.setSettings(&settings); event.Type == protocol.EvtError {
		t.Fatal(event.Error)
	}
	h.m.takeQuestion(context.Background(), h.asking("q1", "go test ./..."))
	if len(h.agents.sent(protocol.ReqAnswer)) != 0 || len(h.cards()) != 1 {
		t.Fatalf("answers %v, cards %v", h.agents.sent(protocol.ReqAnswer), h.cards())
	}
}

// Anything else goes in front of the user as a card with the agent's own
// options, and answering the card answers the agent.
func TestARiskyRequestBecomesACardTheUserAnswers(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	h.agents.answer = delivered
	h.m.takeQuestion(context.Background(), h.asking("q1", "git push origin main"))

	if len(h.agents.sent(protocol.ReqAnswer)) != 0 {
		t.Fatal("a push was approved without the user")
	}
	cards := h.cards()
	if len(cards) != 1 {
		t.Fatalf("cards = %+v", cards)
	}
	card := cards[0].Card
	if card.Kind != protocol.CardQuestion || card.State != protocol.CardOpen || card.Title != "wants to run a command" ||
		card.Detail != "git push origin main" || card.Reason != "it pushes to a remote" || len(card.Options) != 3 || card.AgentName != "api" {
		t.Fatalf("card = %+v", card)
	}

	reply := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerCardReply, CardID: card.ID, OptionKey: "1"})
	if reply.Type != protocol.EvtManagerEntry || reply.Entry.Card.State != protocol.CardAnswered || reply.Entry.Card.Outcome != "You chose “Yes”" {
		t.Fatalf("reply = %+v", reply)
	}
	answers := h.agents.sent(protocol.ReqAnswer)
	if len(answers) != 1 || answers[0].QuestionID != "q1" || answers[0].OptionKey != "1" {
		t.Fatalf("answers = %+v", answers)
	}
	again := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerCardReply, CardID: card.ID, OptionKey: "1"})
	if again.Type != protocol.EvtError {
		t.Fatal("a card answered twice")
	}
}

// The user answers the agent themselves, in its terminal, its thread or on
// the phone: the card goes, without anyone touching it.
func TestACardGoesWhenTheUserAnswersTheAgentThemselves(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	h.agents.answer = delivered
	h.m.NeedsAnswer(h.asking("q1", "git push origin main"))
	waitFor(t, "the card", func() bool { return len(h.m.conversation.openCards()) == 1 })

	h.m.reconcile(context.Background(), h.answered())
	if open := h.m.conversation.openCards(); len(open) != 0 {
		t.Fatalf("still open: %+v", open)
	}
	cards := h.cards()
	last := cards[len(cards)-1].Card
	if last.State != protocol.CardSettled || last.Outcome != "Answered in the agent" {
		t.Fatalf("the card came back as %+v", last)
	}
	if len(h.agents.sent(protocol.ReqAnswer)) != 0 {
		t.Fatal("Agentman answered a question the user already had")
	}

	// A card for a question already answered elsewhere cannot answer the
	// agent's next one.
	h.m.NeedsAnswer(h.asking("q2", "git push --force"))
	waitFor(t, "the second card", func() bool { return len(h.m.conversation.openCards()) == 1 })
	reply := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerCardReply, CardID: last.ID, OptionKey: "1"})
	if reply.Type != protocol.EvtError || len(h.agents.sent(protocol.ReqAnswer)) != 0 {
		t.Fatalf("an old card answered a new question: %+v", reply)
	}
}

// A question that flickers off for a sweep and comes back gets its card
// back.
func TestAQuestionThatComesBackGetsItsCardBack(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	h.m.NeedsAnswer(h.asking("q1", "git push"))
	waitFor(t, "the card", func() bool { return len(h.m.conversation.openCards()) == 1 })
	h.m.reconcile(context.Background(), h.answered())
	h.m.NeedsAnswer(h.asking("q1", "git push"))
	waitFor(t, "the card again", func() bool { return len(h.m.conversation.openCards()) == 1 })
}

// An agent that needs the user for something in words gets a card with a
// box to reply. If the user writes to the agent directly, the card goes.
func TestANeedsYouCardGoesWhenTheUserWritesToTheAgent(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	site := h.agents.Sessions()[0]
	site.State = protocol.StateIdle
	h.agents.setSessions([]protocol.Session{site})
	h.m.reconcile(context.Background(), h.agents.Sessions())

	h.m.cardMu.Lock()
	h.m.postNeedsYouLocked(site, protocol.Brief{SessionID: site.ID, NeedsYou: "Approve the deploy to production?", Summary: "Built and tested."})
	h.m.cardMu.Unlock()
	open := h.m.conversation.openCards()
	if len(open) != 1 || open[0].Card.Kind != protocol.CardNeedsYou || open[0].Card.Title != "Approve the deploy to production?" {
		t.Fatalf("open = %+v", open)
	}

	var messages []protocol.Message
	h.agents.answer = func(req protocol.Request) protocol.Event {
		return protocol.Event{Type: protocol.EvtPage, Page: &protocol.Page{SessionID: req.SessionID, Messages: messages}}
	}
	// It picks up on its own, with nothing from the user: the card stays.
	site.State = protocol.StateBusy
	h.agents.setSessions([]protocol.Session{site})
	h.m.reconcile(context.Background(), h.agents.Sessions())
	if len(h.m.conversation.openCards()) != 1 {
		t.Fatal("the card went though the user never wrote")
	}

	// The user writes to it in its terminal, and it picks up again.
	messages = []protocol.Message{{ID: "u", Role: protocol.RoleUser, Ts: time.Now().UnixMilli() + 1, Text: "yes, ship it"}}
	site.State = protocol.StateIdle
	h.agents.setSessions([]protocol.Session{site})
	h.m.reconcile(context.Background(), h.agents.Sessions())
	site.State = protocol.StateBusy
	h.agents.setSessions([]protocol.Session{site})
	h.m.reconcile(context.Background(), h.agents.Sessions())
	if open := h.m.conversation.openCards(); len(open) != 0 {
		t.Fatalf("still open: %+v", open)
	}
}

// Replying on the card sends the reply to the agent.
func TestReplyingOnACardMessagesTheAgent(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	h.agents.answer = func(req protocol.Request) protocol.Event {
		event := delivered(req)
		event.Status = protocol.StatusQueued
		return event
	}
	site := h.agents.Sessions()[0]
	h.m.cardMu.Lock()
	h.m.postNeedsYouLocked(site, protocol.Brief{NeedsYou: "Which colour?"})
	h.m.cardMu.Unlock()
	card := h.m.conversation.openCards()[0].Card

	reply := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerCardReply, CardID: card.ID, Text: "Blue"})
	if reply.Type != protocol.EvtManagerEntry || !strings.HasPrefix(reply.Entry.Card.Outcome, "You replied: “Blue”") ||
		!strings.Contains(reply.Entry.Card.Outcome, "when its turn ends") {
		t.Fatalf("reply = %+v", reply)
	}
	if sent := h.agents.sent(protocol.ReqSendMessage); len(sent) != 1 || sent[0].Text != "Blue" || sent[0].SessionID != "claude:a" {
		t.Fatalf("sent = %+v", sent)
	}
}

// The user can put a card away, and Agentman can take one away when it
// notices the user dealt with it.
func TestCardsCanBePutAway(t *testing.T) {
	model := &scriptedModel{}
	h := newHarness(t, model, true)
	session := h.asking("q1", "git push")
	h.m.takeQuestion(context.Background(), session)
	first := h.m.conversation.openCards()[0].Card.ID
	if event := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerCardDismiss, CardID: first}); event.Entry.Card.State != protocol.CardDismissed {
		t.Fatalf("dismiss = %+v", event)
	}

	h.m.takeQuestion(context.Background(), h.asking("q2", "git push --tags"))
	second := h.m.conversation.openCards()[0].Card.ID
	model.replies = []string{
		toolReply("t1", "dismiss_card", `{"card":"`+second+`","reason":"answered in the agent"}`),
		textReply("Done."),
	}
	h.m.send(context.Background(), "I already answered api")
	h.waitIdle(t)
	if open := h.m.conversation.openCards(); len(open) != 0 {
		t.Fatalf("still open: %+v", open)
	}
	if !slicesContain(roles(h.entries()), "activity") {
		t.Fatal("taking the card away wasn't shown")
	}
}

// Asked to answer something the rules don't approve, Agentman marks its
// pick on the card instead. A no, or a low-risk yes, goes straight through.
func TestAnsweringForTheUserFollowsTheRules(t *testing.T) {
	model := &scriptedModel{replies: []string{
		toolReply("t1", "answer_question", `{"agent":"api","option":"Yes"}`),
		textReply("It's on the card."),
	}}
	h := newHarness(t, model, true)
	h.agents.answer = delivered
	h.asking("q1", "git push origin main")
	h.m.send(context.Background(), "approve api's push")
	h.waitIdle(t)
	if len(h.agents.sent(protocol.ReqAnswer)) != 0 {
		t.Fatal("the model approved a push")
	}
	open := h.m.conversation.openCards()
	if len(open) != 1 || open[0].Card.Suggested != "1" {
		t.Fatalf("open = %+v", open)
	}

	model.replies = append(model.replies,
		toolReply("t2", "answer_question", `{"agent":"api","option":"3","text":"push to a branch instead"}`),
		textReply("Told it no."),
	)
	h.m.send(context.Background(), "no, tell it to use a branch")
	h.waitIdle(t)
	answers := h.agents.sent(protocol.ReqAnswer)
	if len(answers) != 1 || answers[0].OptionKey != "3" || answers[0].AnswerText != "push to a branch instead" {
		t.Fatalf("answers = %+v", answers)
	}
	if len(h.m.conversation.openCards()) != 0 {
		t.Fatal("the card stayed up after Agentman answered")
	}
}

func TestAgentmanMessagesAnAgent(t *testing.T) {
	model := &scriptedModel{replies: []string{
		toolReply("t1", "send_to_agent", `{"agent":"site","text":"commit what you have"}`),
		textReply("Sent."),
	}}
	h := newHarness(t, model, true)
	sessions := h.agents.Sessions()
	sessions[0].Inject = protocol.InjectTmux
	h.agents.setSessions(sessions)
	h.agents.answer = delivered
	h.m.send(context.Background(), "tell site to commit")
	h.waitIdle(t)
	sent := h.agents.sent(protocol.ReqSendMessage)
	if len(sent) != 1 || sent[0].Text != "commit what you have" || sent[0].SessionID != "claude:a" {
		t.Fatalf("sent = %+v", sent)
	}
	var shown bool
	for _, entry := range h.entries() {
		shown = shown || (entry.Role == "activity" && entry.Text == "Sent site: “commit what you have”")
	}
	if !shown {
		t.Fatalf("what was sent wasn't shown: %+v", h.entries())
	}
}

// Until the user sets Agentman up, it does nothing with questions.
func TestWithoutAKeyQuestionsAreLeftAlone(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, false)
	h.agents.answer = delivered
	h.m.takeQuestion(context.Background(), h.asking("q1", "go test ./..."))
	h.m.takeQuestion(context.Background(), h.asking("q2", "git push"))
	if len(h.agents.sent(protocol.ReqAnswer)) != 0 || len(h.entries()) != 0 {
		t.Fatal("Agentman acted without being set up")
	}
}

// What happened between messages reaches the model with the next one, and
// the user's own entry stays as they wrote it.
func TestEventsReachTheModelWithTheNextMessage(t *testing.T) {
	model := &scriptedModel{replies: []string{textReply("Noted.")}}
	h := newHarness(t, model, true)
	h.agents.answer = delivered
	h.m.takeQuestion(context.Background(), h.asking("q1", "go test ./..."))
	h.m.send(context.Background(), "anything new?")
	h.waitIdle(t)

	messages := model.request(0).Messages
	last := messages[len(messages)-1]
	if len(last.Content) != 2 || last.Content[0].OfText == nil || last.Content[1].OfText == nil {
		t.Fatalf("last message = %+v", last)
	}
	if notes := last.Content[0].OfText.Text; !strings.Contains(notes, "<events") || !strings.Contains(notes, "Approved for api") {
		t.Fatalf("notes = %q", notes)
	}
	if last.Content[1].OfText.Text != "anything new?" {
		t.Fatalf("message = %q", last.Content[1].OfText.Text)
	}
	for _, entry := range h.entries() {
		if entry.Role == "user" && entry.Text != "anything new?" {
			t.Fatalf("the user's entry = %q", entry.Text)
		}
	}

	model.replies = append(model.replies, textReply("Still nothing."))
	h.m.send(context.Background(), "and now?")
	h.waitIdle(t)
	raw, _ := json.Marshal(model.request(1).Messages[len(model.request(1).Messages)-1])
	if strings.Contains(string(raw), "<events") {
		t.Fatal("the same events were told twice")
	}
}

// A card's latest state is what a restart loads, in the place it was first
// shown.
func TestASettledCardStaysSettledAfterARestart(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	h.m.takeQuestion(context.Background(), h.asking("q1", "git push"))
	h.m.reconcile(context.Background(), h.answered())

	again := h.newManager(t)
	entries, _ := again.conversation.page("", 50)
	if len(entries) != 1 || entries[0].Card == nil || entries[0].Card.State != protocol.CardSettled {
		t.Fatalf("after a restart: %+v", entries)
	}
	if len(again.conversation.openCards()) != 0 {
		t.Fatal("a settled card came back open")
	}
}

func slicesContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
