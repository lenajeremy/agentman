package parser

import (
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Shaped like the records Claude Code 2.1 writes. A plan and a question were
// shown as clipped JSON, and notices the CLI shows the person (a compacted
// conversation, a usage limit, a model switched after a refusal) were dropped.

func TestClaudePlanAndQuestionRowsReadAsText(t *testing.T) {
	path := writeFixture(t, []any{
		claudeAssistant("a1", []any{
			obj{"type": "tool_use", "id": "toolu_plan", "name": "ExitPlanMode", "input": obj{
				"plan":         "# Folder filter: every agent that has run in a directory\n\n## Context\n\nThe Agents screen answers…",
				"planFilePath": "/Users/me/.claude/plans/folder-filter.md",
			}},
			obj{"type": "tool_use", "id": "toolu_ask", "name": "AskUserQuestion", "input": obj{
				"questions": []any{
					obj{"question": "Where should the database live?", "header": "Database", "multiSelect": false,
						"options": []any{obj{"label": "Neon", "description": "Hosted."}, obj{"label": "Docker", "description": "Local."}}},
					obj{"question": "Which region?", "header": "Region", "multiSelect": false,
						"options": []any{obj{"label": "EU"}, obj{"label": "US"}}},
				},
			}},
		}),
	})
	msgs := pageAll(t, path, NewClaudeParser("claude:s"), 20)
	if len(msgs) != 2 {
		t.Fatalf("got %d rows: %+v", len(msgs), msgs)
	}
	if got := msgs[0].Tool.Summary; got != "Folder filter: every agent that has run in a directory" {
		t.Errorf("plan summary = %q", got)
	}
	if got := msgs[1].Tool.Summary; got != "Where should the database live? (+1 more)" {
		t.Errorf("question summary = %q", got)
	}
}

func TestClaudeNoticesBecomeSystemRows(t *testing.T) {
	path := writeFixture(t, []any{
		obj{"type": "system", "subtype": "compact_boundary", "uuid": "s1", "content": "Conversation compacted",
			"level": "info", "compactMetadata": obj{"trigger": "auto", "preTokens": 1000637}},
		obj{"type": "system", "subtype": "informational", "uuid": "s2", "level": "notice",
			"content": "Usage limit reached · continuing automatically at 3:30pm · esc to cancel"},
		obj{"type": "system", "subtype": "model_refusal_fallback", "uuid": "s3", "level": "warning",
			"content":       "The safeguards flagged this message. Retrying with another model.",
			"originalModel": "claude-x", "fallbackModel": "claude-y"},
		// Bookkeeping and command scaffolding stay out.
		obj{"type": "system", "subtype": "turn_duration", "uuid": "s4", "durationMs": 3662023, "messageCount": 511},
		obj{"type": "system", "subtype": "stop_hook_summary", "uuid": "s5", "level": "suggestion", "hookCount": 1},
		obj{"type": "system", "subtype": "local_command", "uuid": "s6", "level": "info",
			"content": "<command-name>/skills</command-name>\n<command-message>skills</command-message>"},
		obj{"type": "system", "subtype": "api_error", "uuid": "s7", "level": "error",
			"error": obj{"message": "Connection error."}, "retryAttempt": 1, "maxRetries": 10},
		// Empty: the explanation follows as an assistant message.
		obj{"type": "system", "subtype": "model_refusal_no_fallback", "uuid": "s8", "level": "warning", "content": ""},
	})
	msgs := pageAll(t, path, NewClaudeParser("claude:s"), 20)
	var got []string
	for _, msg := range msgs {
		if msg.Role != protocol.RoleSystem {
			t.Errorf("row %q has role %q", msg.Text, msg.Role)
		}
		got = append(got, msg.ID+"="+msg.Text)
	}
	want := []string{
		"s1=Conversation compacted",
		"s2=Usage limit reached · continuing automatically at 3:30pm · esc to cancel",
		"s3=The safeguards flagged this message. Retrying with another model.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("system rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
