package source

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func queueLine(t *testing.T, operation string, content any) string {
	t.Helper()
	record := map[string]any{"type": "queue-operation", "operation": operation, "timestamp": "2026-10-19T20:00:00Z"}
	if content != nil {
		record["content"] = content
	}
	line, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return string(line) + "\n"
}

func appendTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, line := range lines {
		if _, err := file.WriteString(line); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClaudeQueueFollowsSentMessagesUntilTaken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	appendTranscript(t, path, `{"type":"user","message":{"role":"user","content":"start"}}`+"\n")
	q := newClaudeQueue()
	if got := q.pending("claude:s", path, true); got != nil {
		t.Fatalf("pending before anything was queued = %q", got)
	}

	appendTranscript(t, path, queueLine(t, "enqueue", "also update the README"))
	if got := q.pending("claude:s", path, true); !slices.Equal(got, []string{"also update the README"}) {
		t.Fatalf("pending after enqueue = %q", got)
	}

	// Taken in the middle of the turn: Claude Code removes it by its text.
	appendTranscript(t, path, `{"type":"queue-operation","operation":"remove","content":"also update the README","reason":"absorbed_mid_turn"}`+"\n")
	if got := q.pending("claude:s", path, true); got != nil {
		t.Fatalf("pending after it was taken = %q", got)
	}
}

func userLine(t *testing.T, content any) string {
	t.Helper()
	line, err := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": content}})
	if err != nil {
		t.Fatal(err)
	}
	return string(line) + "\n"
}

func TestClaudeQueueHidesItsOwnTraffic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	appendTranscript(t, path,
		queueLine(t, "enqueue", "first"),
		queueLine(t, "enqueue", "<task-notification>\n<task-id>b1</task-id>\n</task-notification>"),
		queueLine(t, "enqueue", "<agent-message from=\"x\">done</agent-message>"),
		queueLine(t, "enqueue", "second"),
	)
	q := newClaudeQueue()
	if got := q.pending("claude:s", path, true); !slices.Equal(got, []string{"first", "second"}) {
		t.Fatalf("pending = %q, want only the user's own messages", got)
	}
}

func TestClaudeQueueClearsAMessageOnceTheTranscriptShowsItRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	appendTranscript(t, path, queueLine(t, "enqueue", "first"), queueLine(t, "enqueue", "second"))
	q := newClaudeQueue()
	q.pending("claude:s", path, true)

	// Read at the end of the turn: a dequeue, then the message as a user
	// record. Dequeue alone says nothing about which one it was.
	appendTranscript(t, path, queueLine(t, "dequeue", nil))
	if got := q.pending("claude:s", path, true); !slices.Equal(got, []string{"first", "second"}) {
		t.Fatalf("pending after a bare dequeue = %q", got)
	}
	appendTranscript(t, path, userLine(t, "second"))
	if got := q.pending("claude:s", path, true); !slices.Equal(got, []string{"first"}) {
		t.Fatalf("pending once \"second\" was read = %q", got)
	}

	// Taken mid-turn: recorded as a queued_command attachment.
	appendTranscript(t, path, `{"type":"attachment","attachment":{"type":"queued_command","prompt":"first","commandMode":"prompt","origin":{"kind":"human"}}}`+"\n")
	if got := q.pending("claude:s", path, true); got != nil {
		t.Fatalf("pending once \"first\" was taken mid-turn = %q", got)
	}
}

func TestClaudeQueueReadsMergedAndReworkedMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	appendTranscript(t, path, queueLine(t, "enqueue", "one"), queueLine(t, "enqueue", "two"), queueLine(t, "enqueue", "three"))
	q := newClaudeQueue()
	q.pending("claude:s", path, true)

	// Two read together, in one record of text blocks.
	appendTranscript(t, path, queueLine(t, "dequeue", nil),
		userLine(t, []any{map[string]any{"type": "text", "text": "one\ntwo"}}))
	if got := q.pending("claude:s", path, true); !slices.Equal(got, []string{"three"}) {
		t.Fatalf("pending after two were read together = %q", got)
	}
	// Read in a form that doesn't contain its text: the dequeue says the
	// oldest one went.
	appendTranscript(t, path, queueLine(t, "dequeue", nil), userLine(t, "[Pasted text #1 +40 lines]"))
	if got := q.pending("claude:s", path, true); got != nil {
		t.Fatalf("pending after the last was read reworded = %q", got)
	}
}

func TestClaudeQueueKeepsMessagesWhenClaudeReadsItsOwnNotification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	appendTranscript(t, path,
		queueLine(t, "enqueue", "<task-notification>done</task-notification>"),
		queueLine(t, "enqueue", "mine"),
		queueLine(t, "dequeue", nil),
		userLine(t, "<task-notification>done</task-notification>"),
		// A tool result is a user record too, and reads nothing.
		userLine(t, []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "ok"}}),
	)
	q := newClaudeQueue()
	if got := q.pending("claude:s", path, true); !slices.Equal(got, []string{"mine"}) {
		t.Fatalf("pending = %q", got)
	}
}

func TestClaudeQueueIsEmptyWhenTheAgentIsNotWorking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	appendTranscript(t, path, queueLine(t, "enqueue", "missed"))
	q := newClaudeQueue()
	if got := q.pending("claude:s", path, false); got != nil {
		t.Fatalf("pending while idle = %q", got)
	}
	// Whatever replay held when the agent stopped was read; it must not
	// come back when the next turn starts.
	appendTranscript(t, path, queueLine(t, "enqueue", "new"))
	if got := q.pending("claude:s", path, true); !slices.Equal(got, []string{"new"}) {
		t.Fatalf("pending in the next turn = %q", got)
	}
}

func TestClaudeQueueWaitsForALineToBeFinished(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	line := queueLine(t, "enqueue", "written in two halves")
	q := newClaudeQueue()
	appendTranscript(t, path, line[:20])
	if got := q.pending("claude:s", path, true); got != nil {
		t.Fatalf("pending from half a line = %q", got)
	}
	appendTranscript(t, path, line[20:])
	if got := q.pending("claude:s", path, true); !slices.Equal(got, []string{"written in two halves"}) {
		t.Fatalf("pending once the line is whole = %q", got)
	}
}

func TestClaudeQueueStartsOverWhenTheTranscriptIsReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	appendTranscript(t, path, queueLine(t, "enqueue", "old file, a long message that makes it bigger"))
	q := newClaudeQueue()
	q.pending("claude:s", path, true)
	if err := os.WriteFile(path, []byte(queueLine(t, "enqueue", "new")), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := q.pending("claude:s", path, true); !slices.Equal(got, []string{"new"}) {
		t.Fatalf("pending after the file was replaced = %q", got)
	}
}

func TestClaudeQueueStartsALargeTranscriptAtALineBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	// One record straddles the start of the window; it must be skipped, not
	// read from its middle.
	filler := `{"type":"assistant","message":{"role":"assistant","content":"` + strings.Repeat("x", queueStartWindow) + `"}}` + "\n"
	appendTranscript(t, path, queueLine(t, "enqueue", "before the window"), filler, queueLine(t, "enqueue", "inside the window"))
	q := newClaudeQueue()
	if got := q.pending("claude:s", path, true); !slices.Equal(got, []string{"inside the window"}) {
		t.Fatalf("pending = %q", got)
	}
}

func TestClaudeQueueForgetsEndedSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	appendTranscript(t, path, queueLine(t, "enqueue", "waiting"))
	q := newClaudeQueue()
	q.pending("claude:s", path, true)
	q.forget(map[string]bool{})
	if len(q.tails) != 0 {
		t.Fatalf("tails after forget = %d", len(q.tails))
	}
	var nilQueue *claudeQueue
	if got := nilQueue.pending("claude:s", path, true); got != nil {
		t.Fatalf("nil queue pending = %q", got)
	}
}
