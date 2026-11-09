package manager

import (
	"context"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func TestTheClosestFolderSetsTheTrust(t *testing.T) {
	settings := protocol.ManagerSettings{AutoApprove: true, Trust: []protocol.FolderTrust{
		{Folder: "/w", Level: protocol.TrustWatch},
		{Folder: "/w/api", Level: protocol.TrustSuggest},
	}}
	for folder, want := range map[string]string{
		"/w/site": protocol.TrustWatch, "/w/api": protocol.TrustSuggest, "/w/api/cmd": protocol.TrustSuggest, "/elsewhere": protocol.TrustHandle,
	} {
		if got := trustLevel(settings, folder); got != want {
			t.Errorf("%s: %s, want %s", folder, got, want)
		}
	}
	settings.AutoApprove = false
	if got := trustLevel(settings, "/elsewhere"); got != protocol.TrustWatch {
		t.Errorf("no entry and approving off: %s", got)
	}
}

func TestTrustIsValidated(t *testing.T) {
	for _, bad := range []protocol.FolderTrust{{Folder: "relative", Level: protocol.TrustWatch}, {Folder: "/w", Level: "everything"}} {
		if validateTrust([]protocol.FolderTrust{bad}) == nil {
			t.Errorf("%+v passed", bad)
		}
	}
}

func askingSession(folder string) protocol.Session {
	return protocol.Session{ID: "a", Kind: "claude", Name: "api", Cwd: folder, State: protocol.StateWaitingInput,
		Inject: protocol.InjectTmux, Question: permission("q1", "npm test")}
}

func setTrust(h *harness, folder, level string) {
	h.m.mu.Lock()
	h.m.settings.Trust = []protocol.FolderTrust{{Folder: folder, Level: level}}
	h.m.mu.Unlock()
}

func TestAFolderOnSuggestGetsAPickNotAnApproval(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	setTrust(h, "/w", protocol.TrustSuggest)
	h.agents.answer = func(protocol.Request) protocol.Event { return protocol.Event{Status: protocol.StatusDelivered} }
	h.m.takeQuestion(context.Background(), askingSession("/w/api"))
	if sent := h.agents.sent(protocol.ReqAnswer); len(sent) != 0 {
		t.Fatalf("approved anyway: %+v", sent)
	}
	cards := h.m.conversation.openCards()
	if len(cards) != 1 || cards[0].Card.Suggested == "" {
		t.Fatalf("%+v", cards)
	}
}

func TestAFolderOnWatchGetsNoPick(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	setTrust(h, "/w", protocol.TrustWatch)
	h.m.takeQuestion(context.Background(), askingSession("/w/api"))
	cards := h.m.conversation.openCards()
	if len(cards) != 1 || cards[0].Card.Suggested != "" || len(h.agents.sent(protocol.ReqAnswer)) != 0 {
		t.Fatalf("%+v", cards)
	}
}

func TestAnApprovalSaysWhatItApproved(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	h.agents.answer = func(protocol.Request) protocol.Event { return protocol.Event{Status: protocol.StatusDelivered} }
	h.m.takeQuestion(context.Background(), askingSession("/w/api"))
	var found *protocol.ApprovedRequest
	for _, entry := range h.entries() {
		if entry.Tool == "approve" {
			found = entry.Approved
		}
	}
	if found == nil || found.Kind != "command" || found.Target != "npm test" || found.Folder != "/w/api" {
		t.Fatalf("%+v", found)
	}
}

func TestOnlySafeThingsAreLearned(t *testing.T) {
	cases := []struct{ kind, target, want string }{
		{"command", "go test ./webhooks/...", "go test*"},
		{"command", "swift build -c release", "swift build*"},
		{"command", "npm run lint -- --fix", "npm run lint -- --fix"},
		{"command", "make e2e", "make e2e"},
		{"command", "go run ./cmd/x", "go run ./cmd/x"},
		{"network", "https://api.github.com/repos", "https://api.github.com/*"},
	}
	for _, target := range []string{
		"git push origin main", "git -C . push origin main", "git -c x=y push --force origin main", "/usr/bin/git push origin main",
		"FOO=1 git push origin main", "env git push origin main", "time git push origin main", "git reset --hard", "git branch -D main",
		"rm -rf build", "/bin/rm -rf build", "env rm -rf build", "find . -delete", "xargs rm -rf", "aws s3 rm s3://b/k --recursive",
		"sudo make install", "env sudo ls", "FOO=1 sudo ls", "nice sudo ls", "/usr/bin/sudo ls",
		"security find-generic-password -s x -w", "aws configure get aws_secret_access_key", "printenv ANTHROPIC_API_KEY", "cat .env", "cat secrets.yaml",
		"curl https://x.dev", "npm publish", "make deploy", "pnpm --filter web deploy", "npm run release", "npx wrangler versions deploy", "bash -c deploy",
		"python3 tools/gen.py", "go test ./... && git push", "go test ./... & git push", "cat $(find .)", "cat ~/.ssh/id_rsa", "echo x > out.txt",
	} {
		cases = append(cases, struct{ kind, target, want string }{"command", target, ""})
	}
	cases = append(cases, struct{ kind, target, want string }{"edit", "/etc/hosts", ""}, struct{ kind, target, want string }{"network", "not a url", ""})
	for _, c := range cases {
		got, ok := learnablePattern(c.kind, c.target)
		if ok != (c.want != "") || got != c.want {
			t.Errorf("%s %q: %q %v, want %q", c.kind, c.target, got, ok, c.want)
		}
	}
}

func TestDecliningInOtherWordsIsNotApproving(t *testing.T) {
	options := []protocol.QuestionOption{{Key: "1", Label: "Skip"}, {Key: "2", Label: "Keep (3)"}, {Key: "3", Label: "Cancel"}, {Key: "4", Label: "Yes"}}
	for key, want := range map[string]bool{"1": false, "2": false, "3": false, "4": true} {
		if isAffirmative(options, key) != want {
			t.Errorf("%s: want %v", key, want)
		}
	}
}

func commandQuestion(command string) *protocol.Question {
	return &protocol.Question{ID: "q", Title: "Bash command", Detail: "Do it\n\n" + command,
		Options: []protocol.QuestionOption{{Key: "1", Label: "Yes"}, {Key: "2", Label: "No"}}}
}

func learnRig(t *testing.T, autopilot protocol.Autopilot) (*harness, protocol.Session) {
	t.Helper()
	h := newHarness(t, &scriptedModel{}, true)
	h.m.mu.Lock()
	h.m.settings.Autopilot = &autopilot
	h.m.mu.Unlock()
	return h, protocol.Session{ID: "a", Name: "api", Cwd: "/w/api", Question: commandQuestion("git add -p")}
}

// git add is safe by the built-in policy, so a command it leaves to the user
// is what's learned here.
func askedCommand() *protocol.Question { return commandQuestion("make e2e") }

func TestAHabitEarnsOneOffer(t *testing.T) {
	h, session := learnRig(t, protocol.Autopilot{LearnRules: true})
	session.Question = askedCommand()
	answer := protocol.Request{SessionID: "a", OptionKey: "1"}
	for i := 0; i < 2; i++ {
		h.m.Answered(session, answer)
	}
	if got := h.m.conversation.openCards(); len(got) != 0 {
		t.Fatalf("offered early: %+v", got)
	}
	h.m.Answered(session, answer)
	cards := h.m.conversation.openCards()
	if len(cards) != 1 || cards[0].Card.Kind != protocol.CardOffer || !strings.Contains(cards[0].Card.Title, "make e2e") {
		t.Fatalf("%+v", cards)
	}
	h.m.Answered(session, answer)
	if got := h.m.conversation.openCards(); len(got) != 1 {
		t.Fatalf("offered twice: %d", len(got))
	}
}

func TestNoLearningUntilTurnedOnAndNotFromDenialsOrItsOwnAnswers(t *testing.T) {
	h, session := learnRig(t, protocol.Autopilot{})
	session.Question = askedCommand()
	for i := 0; i < 4; i++ {
		h.m.Answered(session, protocol.Request{OptionKey: "1"})
	}
	if got := h.m.conversation.openCards(); len(got) != 0 {
		t.Fatalf("learned while off: %+v", got)
	}
	h, session = learnRig(t, protocol.Autopilot{LearnRules: true})
	session.Question = askedCommand()
	for i := 0; i < 4; i++ {
		h.m.Answered(session, protocol.Request{OptionKey: "2"})
		h.m.Answered(session, protocol.Request{OptionKey: "1", ClientID: "agentman-x"})
	}
	if got := h.m.conversation.openCards(); len(got) != 0 {
		t.Fatalf("learned from a denial or its own answer: %+v", got)
	}
}

func takeOffer(t *testing.T, h *harness, session protocol.Session, option string) protocol.Event {
	t.Helper()
	session.Question = askedCommand()
	for i := 0; i < 3; i++ {
		h.m.Answered(session, protocol.Request{OptionKey: "1"})
	}
	cards := h.m.conversation.openCards()
	if len(cards) != 1 {
		t.Fatalf("%+v", cards)
	}
	return h.m.replyToCard(context.Background(), protocol.Request{CardID: cards[0].Card.ID, OptionKey: option})
}

func TestTakingTheOfferMakesARuleThatApproves(t *testing.T) {
	h, session := learnRig(t, protocol.Autopilot{LearnRules: true})
	if event := takeOffer(t, h, session, "here"); event.Type == protocol.EvtError {
		t.Fatal(event.Error)
	}
	rules := h.m.currentSettings().Rules
	if len(rules) != 1 || rules[0].Action != "allow" || rules[0].Folder != "/w/api" || rules[0].Pattern != "make e2e" {
		t.Fatalf("%+v", rules)
	}
	d := classify(protocol.Session{Cwd: "/w/api"}, askedCommand(), policyOf(h.m.currentSettings()))
	if !d.auto {
		t.Fatalf("the rule doesn't approve it: %+v", d)
	}
	if d := classify(protocol.Session{Cwd: "/w/site"}, askedCommand(), policyOf(h.m.currentSettings())); d.auto {
		t.Fatal("the folder's rule reached another folder")
	}
}

func TestLeavingTheOfferChangesNothing(t *testing.T) {
	h, session := learnRig(t, protocol.Autopilot{LearnRules: true})
	if event := takeOffer(t, h, session, "no"); event.Type == protocol.EvtError {
		t.Fatal(event.Error)
	}
	if rules := h.m.currentSettings().Rules; len(rules) != 0 {
		t.Fatalf("%+v", rules)
	}
}

func TestWhatTheRulesAlreadyApproveIsNotOffered(t *testing.T) {
	h, session := learnRig(t, protocol.Autopilot{LearnRules: true})
	session.Question = commandQuestion("go test ./...")
	for i := 0; i < 4; i++ {
		h.m.Answered(session, protocol.Request{OptionKey: "1"})
	}
	if got := h.m.conversation.openCards(); len(got) != 0 {
		t.Fatalf("offered what's already approved: %+v", got)
	}
}

func TestAnOfferOfARuleOutlivesItsAgent(t *testing.T) {
	h, session := learnRig(t, protocol.Autopilot{LearnRules: true})
	session.Question = askedCommand()
	for i := 0; i < 3; i++ {
		h.m.Answered(session, protocol.Request{OptionKey: "1"})
	}
	h.m.reconcile(context.Background(), nil)
	if got := h.m.conversation.openCards(); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestTheModelCantApproveInAFolderNotOnHandle(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	setTrust(h, "/w", protocol.TrustWatch)
	session := askingSession("/w/api")
	h.agents.setSessions([]protocol.Session{session})
	h.agents.answer = func(protocol.Request) protocol.Event { return protocol.Event{Status: protocol.StatusDelivered} }
	result := runAnswerQuestion(context.Background(), h.m, map[string]any{"agent": "a", "option": "1"})
	if result.isError {
		t.Logf("%s", result.text)
	}
	if sent := h.agents.sent(protocol.ReqAnswer); len(sent) != 0 {
		t.Fatalf("approved in a watched folder: %+v", sent)
	}
	setTrust(h, "/w", protocol.TrustHandle)
	runAnswerQuestion(context.Background(), h.m, map[string]any{"agent": "a", "option": "1"})
	if sent := h.agents.sent(protocol.ReqAnswer); len(sent) != 1 {
		t.Fatalf("a folder on Handle should allow it: %+v", sent)
	}
}
