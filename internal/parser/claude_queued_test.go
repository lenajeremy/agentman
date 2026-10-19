package parser

import (
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Shaped like the records Claude Code writes for a message the user sent
// while it was working. Read mid-turn, such a message is recorded only as a
// "queued_command" attachment, so it never reached the conversation. The same
// attachment carries reports Claude's own agents hand back and background
// task notifications, which nobody typed and must stay out.

func queuedAttachment(uuid string, attachment obj) obj {
	return obj{
		"type": "attachment", "uuid": uuid, "timestamp": "2026-10-19T20:00:00.000Z",
		"isSidechain": false, "attachment": attachment,
	}
}

func TestClaudeMessageTakenMidTurnIsAUserRow(t *testing.T) {
	path := writeFixture(t, []any{
		obj{"type": "queue-operation", "operation": "enqueue", "content": "also update the README"},
		obj{"type": "queue-operation", "operation": "remove", "content": "also update the README", "reason": "absorbed_mid_turn"},
		queuedAttachment("q1", obj{
			"type": "queued_command", "prompt": "also update the README", "commandMode": "prompt",
			"origin": obj{"kind": "human"}, "source_uuid": "s1", "delivery_id": "d1",
		}),
	})
	msgs := pageAll(t, path, NewClaudeParser("claude:s"), 20)
	if len(msgs) != 1 {
		t.Fatalf("got %d rows: %+v", len(msgs), msgs)
	}
	got := msgs[0]
	if got.ID != "q1" || got.Role != protocol.RoleUser || got.Text != "also update the README" {
		t.Fatalf("row = %+v", got)
	}
}

func TestClaudeMessageTakenMidTurnKeepsItsImages(t *testing.T) {
	path := writeFixture(t, []any{
		queuedAttachment("q2", obj{
			"type": "queued_command", "commandMode": "prompt", "origin": obj{"kind": "human"},
			"prompt": []any{
				obj{"type": "text", "text": "what's wrong here?"},
				obj{"type": "image", "source": obj{"type": "base64", "media_type": "image/png", "data": "AAAA"}},
			},
		}),
	})
	msgs := pageAll(t, path, NewClaudeParser("claude:s"), 20)
	if len(msgs) != 1 || msgs[0].Text != "what's wrong here?\n[image]" {
		t.Fatalf("rows = %+v", msgs)
	}
}

func TestClaudeQueuedTrafficNobodyTypedStaysOut(t *testing.T) {
	path := writeFixture(t, []any{
		// A report one of Claude's own agents handed back.
		queuedAttachment("p1", obj{
			"type": "queued_command", "prompt": "[Subagent hand-back] report", "commandMode": "prompt",
			"origin": obj{"kind": "peer", "handback": true}, "isMeta": true,
		}),
		// A background task finishing.
		queuedAttachment("n1", obj{
			"type": "queued_command", "prompt": "<task-notification>done</task-notification>",
			"commandMode": "task-notification", "origin": obj{"kind": "task-notification"},
		}),
		// Other attachment kinds are bookkeeping.
		queuedAttachment("o1", obj{"type": "todo_reminder", "content": []any{}}),
	})
	if msgs := pageAll(t, path, NewClaudeParser("claude:s"), 20); len(msgs) != 0 {
		t.Fatalf("rows = %+v, want none", msgs)
	}
}
