package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Codex's question rows are the only ones it writes before their outcome.
// Paging backwards meets the answer first; a page that stopped between the
// answer and the question left the question "running" on the next page, whose
// parser never saw the answer. The answer does not say which call it belongs
// to, so the page reads on until every answer it met has met its call.
func TestACodexPageBoundaryBetweenAQuestionAndItsAnswerLeavesNothingRunning(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "rollout.jsonl")
	lines := []string{
		`{"timestamp":"2026-10-01T10:00:00Z","type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"call_Q","arguments":"{\"questions\":[{\"title\":\"Which option?\"}]}"}}`,
		`{"timestamp":"2026-10-01T10:00:01Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"call_X","arguments":"{\"cmd\":\"ls\"}"}}`,
		`{"timestamp":"2026-10-01T10:00:02Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"m1","content":[{"type":"text","text":"Looking."}]}}}`,
		`{"timestamp":"2026-10-01T10:00:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_X","output":"a.txt"}}`,
		`{"timestamp":"2026-10-01T10:00:04Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_Q","output":"{\"answer\":\"A\"}"}}`,
		`{"timestamp":"2026-10-01T10:00:05Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"m2","content":[{"type":"text","text":"Going with A."}]}}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	const id = "codex:s1"
	src.sessions[id] = codexSession{transcript: path}

	status := map[string]protocol.ToolStatus{}
	seen := map[string]int{}
	before := ""
	for range 6 {
		page, err := src.Page(context.Background(), id, before, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range page.Messages {
			seen[message.ID]++
			if message.Tool != nil {
				status[message.ID] = message.Tool.Status
			}
		}
		if !page.HasMore {
			break
		}
		before = page.NextCursor
	}
	if got := status["codex-question:call_Q"]; got != protocol.ToolOK {
		t.Errorf("the question paged in as %q, want ok", got)
	}
	for _, want := range []string{"m1", "m2", "codex-question:call_Q"} {
		if seen[want] != 1 {
			t.Errorf("%s appeared %d times, want once", want, seen[want])
		}
	}
}
