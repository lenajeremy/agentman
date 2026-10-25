package parser

import (
	"encoding/json"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Shaped like what Claude Code 2.1 writes when the daemon pastes a message of
// several lines with two images ahead of it: the first image taken in as
// "[Image #9]" with its file named in a meta record after it, the rest of the
// paste wrapped in pasted_content tags, the second image still a path in it.

const (
	keptA = "/Users/me/.agentman/images/claude-s/a.png"
	keptB = "/Users/me/.agentman/images/claude-s/b.jpg"
)

func pastedPrompt() []any {
	return []any{
		obj{
			"type": "user", "uuid": "u1", "promptId": "p1", "timestamp": "2026-10-25T09:00:00.000Z",
			"imagePasteIds": []any{9},
			"message": obj{"role": "user", "content": []any{
				obj{"type": "text", "text": "[Image #9]\n\n<pasted_content id=\"334c\">\n" + keptB +
					" Clean this up.\n\n1. The sidebar.\n2. The toolbar.\n</pasted_content id=\"334c\">\n"},
				obj{"type": "image", "source": obj{"type": "base64", "media_type": "image/png", "data": "AAAA"}},
			}},
		},
		obj{
			"type": "user", "uuid": "u2", "promptId": "p1", "isMeta": true, "timestamp": "2026-10-25T09:00:00.000Z",
			"message": obj{"role": "user", "content": []any{
				obj{"type": "text", "text": "[Image: source: " + keptA + "]"},
			}},
		},
	}
}

const pastedWant = keptA + " " + keptB + " Clean this up.\n\n1. The sidebar.\n2. The toolbar."

func TestClaudePastedImagesReadBackwards(t *testing.T) {
	msgs := pageAll(t, writeFixture(t, pastedPrompt()), NewClaudeParser("claude:s"), 20)
	if len(msgs) != 1 || msgs[0].Role != protocol.RoleUser || msgs[0].Text != pastedWant {
		t.Fatalf("rows = %+v", msgs)
	}
}

func TestClaudePastedImagesReadForwards(t *testing.T) {
	p := NewClaudeParser("claude:s")
	rows := map[string]protocol.Message{}
	var order []string
	for i, rec := range pastedPrompt() {
		line, _ := json.Marshal(rec)
		for _, m := range p.Parse(string(line), int64(i)) {
			if _, ok := rows[m.ID]; !ok {
				order = append(order, m.ID)
			}
			rows[m.ID] = m
		}
	}
	if len(order) != 1 || rows[order[0]].Text != pastedWant {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestClaudePasteTagsComeOff(t *testing.T) {
	path := writeFixture(t, []any{
		claudeUser("u1", "\n\n<pasted_content id=\"1\">\nFix the build.\n\nThen run the tests.\n</pasted_content id=\"1\">\n"),
		claudeUser("u2", "Look at this:\n\n<pasted_content id=\"2\">\nerror: x\n</pasted_content id=\"2\">\nThanks"),
	})
	msgs := pageAll(t, path, NewClaudeParser("claude:s"), 20)
	if len(msgs) != 2 || msgs[0].Text != "Fix the build.\n\nThen run the tests." || msgs[1].Text != "Look at this:\n\nerror: x\nThanks" {
		t.Fatalf("rows = %+v", msgs)
	}
}

func TestClaudeImageWithoutAUsablePathKeepsItsMark(t *testing.T) {
	records := []any{
		obj{
			"type": "user", "uuid": "u1", "promptId": "p1", "timestamp": "2026-10-25T09:00:00.000Z",
			"message": obj{"role": "user", "content": "[Image #1] [Image #2] what changed?"},
		},
		obj{
			"type": "user", "uuid": "u2", "promptId": "p1", "isMeta": true, "timestamp": "2026-10-25T09:00:00.000Z",
			"message": obj{"role": "user", "content": []any{
				obj{"type": "text", "text": "[Image: source: /Users/me/Desktop/Screen Shot.png]"},
				obj{"type": "text", "text": "[Image: source: " + keptA + "]"},
			}},
		},
	}
	msgs := pageAll(t, writeFixture(t, records), NewClaudeParser("claude:s"), 20)
	if len(msgs) != 1 || msgs[0].Text != "[Image #1] "+keptA+" what changed?" {
		t.Fatalf("rows = %+v", msgs)
	}
}
