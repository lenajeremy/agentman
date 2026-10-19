package source

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lenajeremy/agentman/internal/parser"
)

func codexUserLine(t *testing.T, text string) string {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"timestamp": "2026-10-19T20:00:00Z", "type": "event_msg",
		"payload": map[string]any{"type": "item_completed", "item": map[string]any{
			"type": "UserMessage", "id": "u1",
			"content": []any{map[string]any{"type": "input_text", "text": text}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(line) + "\n"
}

func newCodexHeld() *heldSends {
	return newHeldSends(func(line []byte) string { return parser.CodexUserText(string(line)) })
}

func TestHeldSendsWaitUntilTheRolloutShowsThemRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	// What was there before the send can't show it read: the same words
	// sent earlier must not clear it.
	appendTranscript(t, path, codexUserLine(t, "run the tests"))
	held := newCodexHeld()
	held.add("codex:a", path, "run the tests")
	held.add("codex:a", path, "and fix the lint")
	if got := held.pending("codex:a", path, true); !slices.Equal(got, []string{"run the tests", "and fix the lint"}) {
		t.Fatalf("pending = %q", got)
	}
	appendTranscript(t, path, `{"type":"event_msg","payload":{"type":"exec_command_end"}}`+"\n", codexUserLine(t, "run the tests"))
	if got := held.pending("codex:a", path, true); !slices.Equal(got, []string{"and fix the lint"}) {
		t.Fatalf("pending after one was read = %q", got)
	}
	// The turn ends: whatever is left was read in some form.
	if got := held.pending("codex:a", path, false); got != nil {
		t.Fatalf("pending once idle = %q", got)
	}
}

func TestHeldSendsReadARolloutThatStartsAfterTheSend(t *testing.T) {
	held := newCodexHeld()
	held.add("codex:b", "", "first message")
	if got := held.pending("codex:b", "", true); !slices.Equal(got, []string{"first message"}) {
		t.Fatalf("pending with no rollout = %q", got)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	appendTranscript(t, path, codexUserLine(t, "first message"))
	if got := held.pending("codex:b", path, true); got != nil {
		t.Fatalf("pending once the new rollout has it = %q", got)
	}
}

func TestHeldSendsForgetEndedSessions(t *testing.T) {
	held := newCodexHeld()
	held.add("codex:c", "", "x")
	held.forget(map[string]bool{})
	if got := held.pending("codex:c", "", true); got != nil {
		t.Fatalf("pending after forget = %q", got)
	}
	var none *heldSends
	none.add("codex:c", "", "x")
	if got := none.pending("codex:c", "", true); got != nil {
		t.Fatalf("nil tracker pending = %q", got)
	}
}
