package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
)

func TestTerminalInjectRechecksLivePaneForQuestion(t *testing.T) {
	pane, err := os.ReadFile(filepath.Join(
		"..", "question", "testdata", "claude_single_real_pane.txt",
	))
	if err != nil {
		t.Fatal(err)
	}
	capture := func(context.Context, string) (string, error) { return string(pane), nil }

	claude := &ClaudeSource{
		capturePane: capture,
		sessions: map[string]claudeSession{
			"claude:s1": {tmuxName: "agentman-claude-test"},
		},
	}
	if _, err := claude.Inject(context.Background(), "claude:s1", "continue"); err == nil || !strings.Contains(err.Error(), "pending question") {
		t.Fatalf("Claude send was not rejected by live question check: %v", err)
	}

	codex := &CodexSource{
		capturePane: capture,
		sessions: map[string]codexSession{
			"codex:s1": {tmuxName: "agentman-codex-test"},
		},
	}
	if _, err := codex.Inject(context.Background(), "codex:s1", "continue"); err == nil || !strings.Contains(err.Error(), "pending question") {
		t.Fatalf("Codex send was not rejected by live question check: %v", err)
	}
}

func TestTerminalInjectFailsClosedWhenPaneCannotBeInspected(t *testing.T) {
	want := errors.New("capture failed")
	capture := func(context.Context, string) (string, error) { return "", want }
	source := &ClaudeSource{
		capturePane: capture,
		sessions: map[string]claudeSession{
			"claude:s1": {tmuxName: "agentman-claude-test"},
		},
	}
	if _, err := source.Inject(context.Background(), "claude:s1", "continue"); err == nil || !errors.Is(err, want) {
		t.Fatalf("send did not fail closed on capture error: %v", err)
	}
}

func claudeMultiQuestion() *question.Question {
	return &question.Question{
		Multiple:    true,
		Custom:      true,
		CustomKey:   "3",
		ChoiceCount: 3,
		FocusIndex:  0,
		CustomIndex: 2,
		Options: []question.Option{
			{Key: "1", Label: "API"},
			{Key: "2", Label: "CLI", Checked: true},
		},
	}
}

func TestTerminalQuestionRevisionTracksDecisionNotFocus(t *testing.T) {
	first := &protocol.Question{
		Prompt: "Choose a database", Title: "Database", Detail: "Question 1 of 2",
		Options: []protocol.QuestionOption{
			{Key: "1", Label: "Postgres", Description: "Hosted", Selected: true, Preview: "preview one"},
			{Key: "2", Label: "SQLite", Description: "Local"},
		},
	}
	second := *first
	second.Options = append([]protocol.QuestionOption(nil), first.Options...)
	second.Options[0].Selected = false
	second.Options[0].Preview = ""
	second.Options[1].Selected = true
	second.Options[1].Preview = "preview two"
	if got, want := terminalQuestionID(first), terminalQuestionID(&second); got != want {
		t.Fatalf("moving terminal focus changed question revision: %q != %q", got, want)
	}

	second.Prompt = "Choose a cache"
	if got, want := terminalQuestionID(first), terminalQuestionID(&second); got == want {
		t.Fatalf("different decisions shared question revision %q", got)
	}
}

func TestClaudeMultiPlanReconcilesCheckboxesBeforeSubmit(t *testing.T) {
	plan, err := planClaudeMultiple(
		claudeMultiQuestion(),
		protocol.QuestionAnswer{Options: []string{"1"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.toggleKeys, []string{"1", "2"}) {
		t.Errorf("toggleKeys = %v, want check 1 and uncheck 2", plan.toggleKeys)
	}
	if plan.safeMove != 0 || plan.targetMove != 3 || plan.afterTextMove != 0 || plan.text != "" {
		t.Errorf("plan = %+v", plan)
	}
}

func TestClaudeMultiPlanLeavesCustomInputBeforeNumericToggles(t *testing.T) {
	current := claudeMultiQuestion()
	current.FocusIndex = current.CustomIndex
	plan, err := planClaudeMultiple(current, protocol.QuestionAnswer{
		Options: []string{"1"},
		Text:    "Desktop",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.toggleKeys, []string{"1", "2"}) {
		t.Errorf("toggleKeys = %v, want check 1 and uncheck 2", plan.toggleKeys)
	}
	// Move from the custom row back to a real option, toggle there, then return
	// to the custom input and finally advance to Submit.
	if plan.safeMove != -2 || plan.targetMove != 2 || plan.afterTextMove != 1 ||
		plan.text != "Desktop" {
		t.Errorf("plan = %+v", plan)
	}
}

func TestClaudeMultiPlanHandlesSubmitFocusAndExistingCustomChoice(t *testing.T) {
	current := claudeMultiQuestion()
	current.FocusIndex = -1
	current.SubmitFocused = true
	current.CustomChecked = true
	plan, err := planClaudeMultiple(current, protocol.QuestionAnswer{Options: []string{"2"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.safeMove != -3 || plan.targetMove != 3 ||
		!reflect.DeepEqual(plan.toggleKeys, []string{"3"}) {
		t.Errorf("plan = %+v, want custom row unchecked from a safe option", plan)
	}
}

func TestClaudeMultiPlanRejectsStaleAndDuplicateOptions(t *testing.T) {
	for _, answer := range []protocol.QuestionAnswer{
		{Options: []string{"9"}},
		{Options: []string{"1", "1"}},
		{},
	} {
		if _, err := planClaudeMultiple(claudeMultiQuestion(), answer); err == nil {
			t.Errorf("answer %+v unexpectedly produced a plan", answer)
		}
	}
}

func TestClaudeMultiPlanUsesRealNavigableRowsAfterTypedCustomAnswer(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(
		"..", "question", "testdata", "claude_multiselect_typed_real_pane.txt",
	))
	if err != nil {
		t.Fatal(err)
	}
	current := question.Detect(string(raw))
	if current == nil {
		t.Fatal("real Claude checkbox form was not detected")
	}
	plan, err := planClaudeMultiple(current, protocol.QuestionAnswer{
		Options: []string{"1", "2", "3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The typed row is still the fourth navigable row even though it is not a
	// listed phone option. Leave it, uncheck its current value, then traverse
	// all four rows to reach the bare Next control.
	if !reflect.DeepEqual(plan.toggleKeys, []string{"4"}) ||
		plan.safeMove != -3 || plan.targetMove != 4 || plan.afterTextMove != 0 {
		t.Errorf("real-pane plan = %+v", plan)
	}
}

func TestClaudeMultiPlanRecognizesAlreadyFocusedRealNextControl(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(
		"..", "question", "testdata", "claude_multiselect_submit_real_pane.txt",
	))
	if err != nil {
		t.Fatal(err)
	}
	current := question.Detect(string(raw))
	if current == nil || !current.SubmitFocused {
		t.Fatalf("real focused-Next frame parsed incorrectly: %+v", current)
	}
	plan, err := planClaudeMultiple(current, protocol.QuestionAnswer{
		Options: []string{"1", "2", "3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.toggleKeys) != 0 || plan.safeMove != 0 || plan.targetMove != 0 {
		t.Errorf("already-focused Next should only need Enter: %+v", plan)
	}
}
