package source

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// Opt in with a disposable Codex pane and its cwd. This drives the current
// CLI's actual queued question through discovery and phone-style answering.
func TestLiveCodexQueuedQuestionDiscoveryAndAnswer(t *testing.T) {
	name, cwd := os.Getenv("AGENTMAN_TEST_CODEX_PANE"), os.Getenv("AGENTMAN_TEST_CODEX_CWD")
	if name == "" || cwd == "" {
		t.Skip("requires a disposable Codex pane with a queued question")
	}
	s, err := NewCodexSource("")
	if err != nil {
		t.Fatal(err)
	}
	s.processCheck = alwaysRunning
	s.listPanes = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{Name: name, Cwd: cwd, Command: "codex", Created: time.Now().Add(-2 * time.Hour)}}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sessions, err := s.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var selectedID string
	for _, session := range sessions {
		if session.Cwd == cwd && session.Question != nil {
			selectedID = session.ID
			if len(session.Question.Options) < 2 {
				t.Fatalf("queued question has no choices: %+v", session.Question)
			}
			answer := protocol.QuestionAnswer{QuestionID: session.Question.ID, OptionKey: session.Question.Options[0].Key}
			if os.Getenv("AGENTMAN_TEST_CODEX_CUSTOM") == "1" {
				if !session.Question.Custom {
					t.Fatal("Codex Other option was not exposed as a custom answer")
				}
				answer.OptionKey, answer.Text = "", "Orange"
			}
			if err := s.Answer(ctx, selectedID, answer); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if selectedID == "" {
		t.Fatalf("Codex's queued question was not discovered: %+v", sessions)
	}
	for {
		pane, err := tmux.Capture(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if question.Detect(pane) == nil && !question.CodexQueued(pane) {
			return
		}
		if ctx.Err() != nil {
			t.Fatal("Codex did not accept the answer")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
