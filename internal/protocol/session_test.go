package protocol

import "testing"

func waiting() Session {
	return Session{
		ID: "codex:cx2", Kind: KindCodex, Name: "agentman", State: StateWaitingInput,
		Question: &Question{
			Prompt: "Run npm test?",
			Options: []QuestionOption{
				{Key: "1", Label: "Yes", Selected: true},
				{Key: "2", Label: "No"},
			},
		},
	}
}

// TestSameAsIgnoresQuestionIdentity is the whole reason SameAs exists.
//
// Discovery reads the pending prompt off the terminal on every sweep and builds
// a new Question from it, so consecutive readings of one unchanged prompt are
// equal in content and different in address. Comparing sessions with == made
// every one of those look like a change: a session blocked on a permission
// prompt pushed an update every second, forever, to a phone on cell data.
func TestSameAsIgnoresQuestionIdentity(t *testing.T) {
	first, second := waiting(), waiting()

	if first.Question == second.Question {
		t.Fatal("the fixtures share a Question pointer, so this proves nothing")
	}
	if first == second {
		t.Fatal("== now compares Questions by value; SameAs may be redundant")
	}
	if !first.SameAs(second) {
		t.Error("two readings of the same unchanged prompt compared different")
	}
}

func TestSameAsDetectsRealChanges(t *testing.T) {
	base := waiting()

	t.Run("the prompt itself", func(t *testing.T) {
		changed := waiting()
		changed.Question.Prompt = "Delete the branch?"
		if base.SameAs(changed) {
			t.Error("a different question was reported as no change; the user would " +
				"be approving something they were never shown")
		}
	})

	t.Run("which option is highlighted", func(t *testing.T) {
		changed := waiting()
		changed.Question.Options[0].Selected = false
		changed.Question.Options[1].Selected = true
		if base.SameAs(changed) {
			t.Error("the moved selection never reached the app")
		}
	})

	t.Run("an option appearing", func(t *testing.T) {
		changed := waiting()
		changed.Question.Options = append(changed.Question.Options, QuestionOption{Key: "3", Label: "Always"})
		if base.SameAs(changed) {
			t.Error("a choice the agent offered was never shown")
		}
	})

	t.Run("the question clearing", func(t *testing.T) {
		answered := waiting()
		answered.Question = nil
		answered.State = StateBusy
		if base.SameAs(answered) {
			t.Error("the prompt stayed on screen after the agent moved on")
		}
		if answered.SameAs(base) {
			t.Error("nil and non-nil compared equal in the other direction")
		}
	})

	t.Run("ordinary fields still count", func(t *testing.T) {
		changed := waiting()
		changed.State = StateBusy
		if base.SameAs(changed) {
			t.Error("a state change was swallowed")
		}
	})
}

func TestSameAsWithoutQuestions(t *testing.T) {
	idle := Session{ID: "codex:cx2", State: StateIdle, LastActivityAt: 1000}
	if !idle.SameAs(idle) {
		t.Error("a session differed from itself")
	}
	// The bug this guards: pane-discovered Codex sessions were stamped with the
	// current time, so an untouched session looked new on every sweep.
	moved := idle
	moved.LastActivityAt = 2000
	if idle.SameAs(moved) {
		t.Error("a genuine activity bump was ignored")
	}
}
