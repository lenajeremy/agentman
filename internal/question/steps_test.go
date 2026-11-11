package question

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTheTabStripReadsAsSteps(t *testing.T) {
	got := parseSteps("←  ☒ Colour  ☐ Size  ☑ Shape  ✔ Submit  →")
	want := []Step{{Title: "Colour", Answered: true}, {Title: "Size"}, {Title: "Shape", Answered: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("steps %v, want %v", got, want)
	}
	if got := parseSteps("←  ☐ Two words  ☐ Next one  ✔ Submit  →"); len(got) != 2 || got[0].Title != "Two words" {
		t.Errorf("titles with spaces: %v", got)
	}
	for _, line := range []string{
		"←  ☐ Colour  ✔ Submit  →", // one question: nothing to move between
		"Which colour?",
		"☐ Colour  ☐ Size", // no arrows: not the strip
		"",
	} {
		if steps := parseSteps(line); steps != nil {
			t.Errorf("%q read as steps %v", line, steps)
		}
	}
}

func setPane(t *testing.T, name string) *Question {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	found := Detect(string(raw))
	if found == nil {
		t.Fatalf("%s: no question", name)
	}
	return found
}

// Screens of a real Claude Code 2.1.322 form of three questions, captured
// while moving through it with the arrow keys.
func TestAFormsStepsAreReadFromRealScreens(t *testing.T) {
	first := setPane(t, "claude_set_first_unanswered_real_pane.txt")
	if first.Prompt != "Which colour?" || len(first.Steps) != 3 || first.Steps[0].Answered || first.Step != 0 {
		t.Errorf("first: prompt %q steps %v step %d", first.Prompt, first.Steps, first.Step)
	}

	second := setPane(t, "claude_set_second_after_answer_real_pane.txt")
	want := []Step{{Title: "Colour", Answered: true}, {Title: "Size"}, {Title: "Shape", Answered: true}}
	if second.Prompt != "Which size?" || !reflect.DeepEqual(second.Steps, want) {
		t.Errorf("second: prompt %q steps %v", second.Prompt, second.Steps)
	}

	back := setPane(t, "claude_set_back_to_answered_real_pane.txt")
	if back.Prompt != "Which shape?" || len(back.Steps) != 3 || !back.Steps[2].Answered {
		t.Errorf("back: prompt %q steps %v", back.Prompt, back.Steps)
	}
	if len(back.Options) < 2 || back.Options[1].Label != "Square" {
		t.Errorf("the earlier answer reads as its label: %v", back.Options)
	}

	// The review is the step past the last question.
	review := setPane(t, "claude_set_review_partly_answered_real_pane.txt")
	if review.Title != "Review your answers" || len(review.Steps) != 3 || review.Step != 4 || !review.Steps[2].Answered {
		t.Errorf("review: title %q steps %v step %d", review.Title, review.Steps, review.Step)
	}

	// The strip doesn't take the question's title any more than before.
	if first.Title != "" {
		t.Errorf("a form's question has no title of its own: %q", first.Title)
	}
}
