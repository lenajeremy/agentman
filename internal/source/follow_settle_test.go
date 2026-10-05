package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

func nextBatch(t *testing.T, ctx context.Context, out <-chan []protocol.Message) []protocol.Message {
	t.Helper()
	select {
	case batch := <-out:
		return batch
	case <-ctx.Done():
		t.Fatal("the follow emitted nothing")
		return nil
	}
}

// Opening a session in the middle of a long command pages its tool row in as
// running, then follows from the end of the file. The result arrived on the
// follow, whose parser had never seen the call, so nothing settled the row
// and it spun until the screen was opened again.
func TestClaudeFollowSettlesACallMadeBeforeTheFollowBegan(t *testing.T) {
	home := t.TempDir()
	src, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "t.jsonl")
	call := `{"type":"assistant","uuid":"a1","timestamp":"2026-10-01T10:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"go test ./..."}}]}}` + "\n"
	result := `{"type":"user","uuid":"u1","timestamp":"2026-10-01T10:01:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(call), 0o600); err != nil {
		t.Fatal(err)
	}
	const id = "claude:s1"
	src.sessions[id] = claudeSession{transcript: path}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out := make(chan []protocol.Message, 4)
	go func() { _ = src.Follow(ctx, id, out) }()
	time.Sleep(2 * followInterval)
	appendLine(t, path, result)

	batch := nextBatch(t, ctx, out)
	if len(batch) != 1 || batch[0].ID != "toolu_1" || batch[0].Tool == nil ||
		batch[0].Tool.Status != protocol.ToolOK || batch[0].Tool.Summary != "go test ./..." {
		t.Fatalf("follow emitted %+v, want toolu_1 settled with its command", batch)
	}
}

// Priming must not replay what was already there: the backlog is Page's.
func TestClaudeFollowStillStartsAtTheEnd(t *testing.T) {
	home := t.TempDir()
	src, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "t.jsonl")
	old := `{"type":"assistant","uuid":"a0","timestamp":"2026-10-01T09:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"earlier"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	const id = "claude:s1"
	src.sessions[id] = claudeSession{transcript: path}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out := make(chan []protocol.Message, 4)
	go func() { _ = src.Follow(ctx, id, out) }()
	time.Sleep(2 * followInterval)
	appendLine(t, path, `{"type":"assistant","uuid":"a2","timestamp":"2026-10-01T10:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"new"}]}}`+"\n")

	batch := nextBatch(t, ctx, out)
	if len(batch) != 1 || batch[0].Text != "new" {
		t.Fatalf("follow emitted %+v, want only the new message", batch)
	}
}

// The Codex counterpart: a question asked before the phone subscribed and
// answered after it.
func TestCodexFollowSettlesAQuestionAskedBeforeTheFollowBegan(t *testing.T) {
	home := t.TempDir()
	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "rollout.jsonl")
	call := `{"timestamp":"2026-10-01T10:00:00Z","type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"call_1","arguments":"{\"questions\":[{\"title\":\"Pick\",\"question\":\"Which?\"}]}"}}` + "\n"
	output := `{"timestamp":"2026-10-01T10:01:00Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"A"}}` + "\n"
	if err := os.WriteFile(path, []byte(call), 0o600); err != nil {
		t.Fatal(err)
	}
	const id = "codex:t1"
	src.sessions[id] = codexSession{transcript: path}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out := make(chan []protocol.Message, 4)
	go func() { _ = src.Follow(ctx, id, out) }()
	time.Sleep(2 * followInterval)
	appendLine(t, path, output)

	batch := nextBatch(t, ctx, out)
	if len(batch) != 1 || batch[0].Tool == nil || batch[0].Tool.Status == protocol.ToolRunning {
		t.Fatalf("follow emitted %+v, want the question settled", batch)
	}
}
