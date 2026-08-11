package source

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
)

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
