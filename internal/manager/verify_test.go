package manager

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

type verifyRig struct {
	h       *harness
	cwd     string
	mu      sync.Mutex
	testErr error
	output  string
	ran     []string
	// during runs as the tests do, to change the world under them.
	during   func()
	messages []protocol.Message
}

func newVerifyRig(t *testing.T, model *scriptedModel, autopilot protocol.Autopilot) *verifyRig {
	t.Helper()
	h := newHarness(t, model, true)
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &verifyRig{h: h, cwd: cwd, messages: []protocol.Message{
		{ID: "1", Role: protocol.RoleUser, Text: "Throttle the webhooks", Ts: 1},
		{ID: "2", Role: protocol.RoleTool, Tool: &protocol.Tool{Name: "Edit", Summary: "limit.go", Status: protocol.ToolOK}, Ts: 2},
		{ID: "3", Role: protocol.RoleAssistant, Text: "Done: 120 a minute.", Ts: 3},
	}}
	h.agents.setSessions([]protocol.Session{{ID: "a", Kind: "claude", Name: "Throttle", Cwd: cwd, State: protocol.StateIdle, Inject: protocol.InjectTmux, LastActivityAt: 5}})
	h.agents.answer = func(req protocol.Request) protocol.Event {
		switch req.Type {
		case protocol.ReqFetchMessages:
			r.mu.Lock()
			defer r.mu.Unlock()
			return protocol.Event{Type: protocol.EvtPage, Page: &protocol.Page{Messages: append([]protocol.Message(nil), r.messages...)}}
		case protocol.ReqSendMessage:
			return protocol.Event{Status: protocol.StatusDelivered}
		case protocol.ReqGitStatus:
			return protocol.Event{Git: &protocol.GitState{Changes: 1}}
		}
		return protocol.Event{}
	}
	h.m.verifyDelay = time.Millisecond
	h.m.runCheck = func(_ context.Context, _ string, program string, args ...string) (string, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.ran = append(r.ran, program+" "+strings.Join(args, " "))
		if program == "git" {
			return "+limit := 120", nil
		}
		if r.during != nil {
			r.during()
		}
		return r.output, r.testErr
	}
	h.m.mu.Lock()
	h.m.settings.Autopilot = &autopilot
	h.m.mu.Unlock()
	return r
}

func (r *verifyRig) check(t *testing.T) (protocol.Verification, bool) {
	t.Helper()
	r.h.m.verifyTurn(context.Background(), "a")
	return r.h.m.verifies.get("a")
}

const verifiedReply = `{"verdict":"verified","summary":"Webhooks are limited to 120 a minute, as asked.","issues":[]}`

func TestChecksAreOffUntilTurnedOn(t *testing.T) {
	r := newVerifyRig(t, &scriptedModel{}, protocol.Autopilot{})
	if _, ok := r.check(t); ok || len(r.ran) != 0 || r.h.model.calls() != 0 {
		t.Fatalf("a check ran while off: %v", r.ran)
	}
}

func TestWorkThatPassesAndMatchesIsVerified(t *testing.T) {
	model := &scriptedModel{replies: []string{textReply(verifiedReply)}}
	r := newVerifyRig(t, model, protocol.Autopilot{Verify: true})
	got, ok := r.check(t)
	if !ok || got.State != protocol.VerifyVerified || got.Command != "go test ./..." || got.MessageID != "3" {
		t.Fatalf("%+v", got)
	}
	if prompt := r.h.model.request(0).Messages[0].Content[0].OfText.Text; !strings.Contains(prompt, "Throttle the webhooks") || !strings.Contains(prompt, "+limit := 120") {
		t.Fatalf("the review didn't see the ask and the diff: %s", prompt)
	}
	// The same turn isn't checked twice.
	if _, _ = r.check(t); len(r.ran) != 2 || model.calls() != 1 {
		t.Fatalf("checked again: %v", r.ran)
	}
}

func TestAConcernIsSaidAsNews(t *testing.T) {
	model := &scriptedModel{replies: []string{textReply(`{"verdict":"concerns","summary":"The retry path isn't limited.","issues":["retry.go skips the limiter"]}`)}}
	r := newVerifyRig(t, model, protocol.Autopilot{Verify: true})
	got, _ := r.check(t)
	if got.State != protocol.VerifyConcerns || len(got.Issues) != 1 {
		t.Fatalf("%+v", got)
	}
	if told := r.h.told(); len(told) != 1 || told[0] != "brief" {
		t.Fatalf("told: %v", told)
	}
}

func TestFailingTestsSkipTheReviewAndGoBackOnlyWhenAllowed(t *testing.T) {
	model := &scriptedModel{}
	r := newVerifyRig(t, model, protocol.Autopilot{Verify: true})
	r.testErr, r.output = errors.New("exit 1"), "--- FAIL: TestLimit"
	got, _ := r.check(t)
	if got.State != protocol.VerifyFailed || got.SentBack || !strings.Contains(got.Output, "TestLimit") || model.calls() != 0 {
		t.Fatalf("%+v, reviews %d", got, model.calls())
	}
	if sent := r.h.agents.sent(protocol.ReqSendMessage); len(sent) != 0 {
		t.Fatalf("sent back without being allowed: %v", sent)
	}
}

func TestFailuresGoBackAtMostTwicePerRequest(t *testing.T) {
	r := newVerifyRig(t, &scriptedModel{}, protocol.Autopilot{Verify: true, SendBack: true})
	r.testErr, r.output = errors.New("exit 1"), "--- FAIL: TestLimit"
	for i, want := range []bool{true, true, false} {
		r.mu.Lock()
		// Each round is the agent's reply to what was sent back, on the
		// same request.
		r.messages = append(r.messages,
			protocol.Message{ID: "s" + string(rune('a'+i)), Role: protocol.RoleUser, Text: sendBackOpening + "go test` failed", Ts: int64(10 + i*2)},
			protocol.Message{ID: "r" + string(rune('a'+i)), Role: protocol.RoleAssistant, Text: "Fixed.", Ts: int64(11 + i*2)})
		r.mu.Unlock()
		got, _ := r.check(t)
		if got.SentBack != want {
			t.Fatalf("round %d: sent back %v, want %v", i, got.SentBack, want)
		}
	}
	if sent := r.h.agents.sent(protocol.ReqSendMessage); len(sent) != 2 || !strings.HasPrefix(sent[0].Text, sendBackOpening) || !strings.Contains(sent[0].Text, "TestLimit") {
		t.Fatalf("sent: %+v", sent)
	}
}

func TestAFailureIsNotSentToAnAgentTheUserIsLookingAt(t *testing.T) {
	r := newVerifyRig(t, &scriptedModel{}, protocol.Autopilot{Verify: true, SendBack: true})
	r.testErr, r.output = errors.New("exit 1"), "FAIL"
	r.h.m.setViewing(context.Background(), "mac", []string{"a"})
	got, _ := r.check(t)
	if got.State != protocol.VerifyFailed || got.SentBack {
		t.Fatalf("%+v", got)
	}
}

func TestATurnThatChangedNothingIsNotChecked(t *testing.T) {
	r := newVerifyRig(t, &scriptedModel{}, protocol.Autopilot{Verify: true})
	r.messages = []protocol.Message{{ID: "1", Role: protocol.RoleUser, Text: "What does this do?", Ts: 1}, {ID: "2", Role: protocol.RoleAssistant, Text: "It limits.", Ts: 2}}
	if _, ok := r.check(t); ok || len(r.ran) != 0 {
		t.Fatalf("checked a question: %v", r.ran)
	}
}

func TestAnAgentThatPicksUpMeanwhileDropsTheCheck(t *testing.T) {
	model := &scriptedModel{}
	r := newVerifyRig(t, model, protocol.Autopilot{Verify: true})
	r.during = func() {
		r.h.agents.setSessions([]protocol.Session{{ID: "a", Kind: "claude", Name: "Throttle", Cwd: r.cwd, State: protocol.StateBusy, Inject: protocol.InjectTmux, LastActivityAt: 9}})
	}
	if got, ok := r.check(t); ok || model.calls() != 0 {
		t.Fatalf("kept a stale check: %+v", got)
	}
}

func TestTheDailyLimitLeavesTheTests(t *testing.T) {
	model := &scriptedModel{replies: []string{textReply(verifiedReply)}}
	r := newVerifyRig(t, model, protocol.Autopilot{Verify: true, VerifyDaily: 1})
	if got, _ := r.check(t); got.State != protocol.VerifyVerified {
		t.Fatalf("%+v", got)
	}
	r.mu.Lock()
	r.messages = append(r.messages, protocol.Message{ID: "4", Role: protocol.RoleTool, Tool: &protocol.Tool{Name: "Edit", Status: protocol.ToolOK}, Ts: 4})
	r.mu.Unlock()
	if got, _ := r.check(t); got.State != protocol.VerifyPassed || model.calls() != 1 {
		t.Fatalf("%+v, reviews %d", got, model.calls())
	}
}

func TestAFolderWithoutTestsIsStillReviewed(t *testing.T) {
	model := &scriptedModel{replies: []string{textReply(verifiedReply)}}
	r := newVerifyRig(t, model, protocol.Autopilot{Verify: true})
	_ = os.Remove(filepath.Join(r.cwd, "go.mod"))
	if got, _ := r.check(t); got.State != protocol.VerifyVerified || got.Command != "" {
		t.Fatalf("%+v", got)
	}
}

func TestTestCommandsAreReadFromTheFolder(t *testing.T) {
	cases := []struct {
		files map[string]string
		want  string
	}{
		{map[string]string{"go.mod": "module x"}, "go test ./..."},
		{map[string]string{"Cargo.toml": ""}, "cargo test"},
		{map[string]string{"package.json": `{"scripts":{"test":"vitest"}}`}, "npm test"},
		{map[string]string{"package.json": `{"scripts":{"test":"vitest"}}`, "pnpm-lock.yaml": ""}, "pnpm test"},
		{map[string]string{"package.json": `{"scripts":{"test":"echo \"Error: no test specified\" && exit 1"}}`}, " "},
		{map[string]string{"package.json": `{"scripts":{"build":"tsc"}}`}, " "},
		{map[string]string{"Package.swift": ""}, "swift test"},
		{map[string]string{"pyproject.toml": "[tool.pytest.ini_options]"}, "python3 -m pytest -q"},
		{map[string]string{"pyproject.toml": "[tool.black]"}, " "},
		{map[string]string{"Makefile": "build:\n\ttrue\ntest: build\n\ttrue\n"}, "make test"},
		{map[string]string{"README.md": ""}, " "},
	}
	for _, c := range cases {
		dir := t.TempDir()
		for name, body := range c.files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		program, args := testCommand(dir)
		if got := program + " " + strings.Join(args, " "); got != c.want {
			t.Errorf("%v: got %q, want %q", c.files, got, c.want)
		}
	}
}

func TestATestRunIsStoppedWithItsChildren(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runCheckProgram(ctx, t.TempDir(), "/bin/sh", "-c", "sleep 30 & wait")
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

func TestChecksSurviveARestartButNotOneUnderWay(t *testing.T) {
	dir := t.TempDir()
	store := loadVerifications(dir)
	store.put(protocol.Verification{SessionID: "a", State: protocol.VerifyVerified, UpdatedAt: 1})
	store.put(protocol.Verification{SessionID: "b", State: protocol.VerifyChecking, UpdatedAt: 1})
	again := loadVerifications(dir)
	if _, ok := again.get("a"); !ok {
		t.Fatal("a finished check was lost")
	}
	if _, ok := again.get("b"); ok {
		t.Fatal("a check under way came back")
	}
}

func TestAnAppThatDoesntKnowAutopilotLeavesItAlone(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	settings := h.m.currentSettings()
	settings.Autopilot = &protocol.Autopilot{Verify: true}
	h.m.setSettings(&settings)
	older := h.m.currentSettings()
	older.Autopilot = nil
	h.m.setSettings(&older)
	if got := h.m.currentSettings().Autopilot; got == nil || !got.Verify {
		t.Fatalf("%+v", got)
	}
}

func TestTestsRunOnlyInAFolderOnHandle(t *testing.T) {
	for _, level := range []string{protocol.TrustWatch, protocol.TrustSuggest} {
		model := &scriptedModel{replies: []string{textReply(verifiedReply)}}
		r := newVerifyRig(t, model, protocol.Autopilot{Verify: true})
		r.h.m.mu.Lock()
		r.h.m.settings.Trust = []protocol.FolderTrust{{Folder: r.cwd, Level: level}}
		r.h.m.mu.Unlock()
		got, _ := r.check(t)
		if got.State != protocol.VerifyVerified || got.Command != "" {
			t.Fatalf("%s: %+v", level, got)
		}
		for _, ran := range r.ran {
			if !strings.HasPrefix(ran, "git diff --no-ext-diff") {
				t.Fatalf("%s: ran %q", level, ran)
			}
		}
	}
}

func TestFailuresGoBackOnlyFromAFolderOnHandle(t *testing.T) {
	r := newVerifyRig(t, &scriptedModel{}, protocol.Autopilot{Verify: true, SendBack: true})
	session, _ := r.h.m.session("a")
	failure := protocol.Verification{Command: "go test", Output: "FAIL"}
	r.h.m.mu.Lock()
	r.h.m.settings.Trust = []protocol.FolderTrust{{Folder: r.cwd, Level: protocol.TrustSuggest}}
	r.h.m.mu.Unlock()
	if r.h.m.sendBackFailure(context.Background(), session, "ask", failure) {
		t.Fatal("sent back from a folder on Suggest")
	}
	r.h.m.mu.Lock()
	r.h.m.settings.Trust = []protocol.FolderTrust{{Folder: r.cwd, Level: protocol.TrustHandle}}
	r.h.m.mu.Unlock()
	if !r.h.m.sendBackFailure(context.Background(), session, "ask", failure) {
		t.Fatal("not sent back from a folder on Handle")
	}
}

func TestWhatAgentmanSendsIsNotANewRequest(t *testing.T) {
	turn := lastTurn([]protocol.Message{
		{ID: "1", Role: protocol.RoleUser, Text: "Do it"},
		{ID: "2", Role: protocol.RoleUser, Text: nudgeText},
		{ID: "3", Role: protocol.RoleUser, Text: headsUpOpening + "Other is changing x."},
		{ID: "4", Role: protocol.RoleAssistant, Text: "On it"},
	})
	if turn.askID != "1" {
		t.Fatalf("%+v", turn)
	}
}

func TestATurnEndingDuringACheckGetsItsOwn(t *testing.T) {
	store := loadVerifications(t.TempDir())
	if !store.begin("a") || store.begin("a") {
		t.Fatal("two at once")
	}
	if !store.end("a") {
		t.Fatal("the turn in between was lost")
	}
	if !store.begin("a") || store.end("a") {
		t.Fatal("ran again for nothing")
	}
}

func TestAMissingTestToolIsNotAFailure(t *testing.T) {
	model := &scriptedModel{replies: []string{textReply(verifiedReply)}}
	r := newVerifyRig(t, model, protocol.Autopilot{Verify: true})
	r.testErr = exec.ErrNotFound
	if got, _ := r.check(t); got.State != protocol.VerifyVerified || got.Command != "" {
		t.Fatalf("%+v", got)
	}
}

func TestAChildThatOutlivesItsTestsCannotHoldTheCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runCheckProgram(ctx, t.TempDir(), "/bin/sh", "-c", "python3 -c 'import os,time; os.setsid(); time.sleep(30)' & wait")
	if err == nil || time.Since(start) > checkKillWait+5*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

func TestOutputCannotCloseItsFence(t *testing.T) {
	if got := longerFence("a ````` b"); got != "``````" {
		t.Fatalf("%q", got)
	}
	if got := longerFence("plain"); got != "```" {
		t.Fatalf("%q", got)
	}
}
