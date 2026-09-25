package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// cursorLine marshals a record the way Cursor writes it: newlines escaped,
// one object per line. Hand-written JSONL fixtures keep growing stray
// braces, so tests build lines this way instead.
func cursorLine(t *testing.T, rec map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func textContent(text string) []any {
	return []any{map[string]any{"type": "text", "text": text}}
}

func TestCursorParseUserUnwrapsEnvelope(t *testing.T) {
	p := NewCursorParser("cursor:s1")
	line := cursorLine(t, map[string]any{
		"role": "user",
		"message": map[string]any{"content": textContent(
			"<timestamp>Monday, Aug 3, 2026, 12:50 PM (UTC+1)</timestamp>\n<user_query>\nwhich directory are you in?\n</user_query>")},
	})
	msgs := p.Parse(line, 0)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != protocol.RoleUser {
		t.Errorf("expected user role, got %q", msgs[0].Role)
	}
	if msgs[0].Text != "which directory are you in?" {
		t.Errorf("unexpected text: %q", msgs[0].Text)
	}
	if msgs[0].ID != "o0" {
		t.Errorf("unexpected id: %q", msgs[0].ID)
	}
}

func TestCursorParseUserWithoutEnvelope(t *testing.T) {
	p := NewCursorParser("cursor:s1")
	line := `{"role":"user","message":{"content":[{"type":"text","text":"hello there"}]}}`
	msgs := p.Parse(line, 42)
	if len(msgs) != 1 || msgs[0].Text != "hello there" {
		t.Fatalf("unexpected messages: %+v", msgs)
	}
	if msgs[0].ID != "o42" {
		t.Errorf("unexpected id: %q", msgs[0].ID)
	}
}

func TestCursorParseUserScaffoldingOnly(t *testing.T) {
	p := NewCursorParser("cursor:s1")
	line := `{"role":"user","message":{"content":[{"type":"text","text":"<timestamp>Monday, Aug 3, 2026, 12:50 PM (UTC+1)</timestamp>"}}]}}`
	if msgs := p.Parse(line, 0); len(msgs) != 0 {
		t.Errorf("scaffolding-only user record should be skipped, got %+v", msgs)
	}
}

func TestCursorParseAssistantTextAndTools(t *testing.T) {
	p := NewCursorParser("cursor:s1")
	line := `{"role":"assistant","message":{"content":[` +
		`{"type":"text","text":"Checking the directory."},` +
		`{"type":"tool_use","name":"Shell","input":{"command":"pwd","description":"Print current working directory"}},` +
		`{"type":"tool_use","name":"Read","input":{"path":"/tmp/main.go"}}]}}`
	msgs := p.Parse(line, 100)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != protocol.RoleAssistant || msgs[0].Text != "Checking the directory." {
		t.Errorf("first message should be assistant text, got %+v", msgs[0])
	}
	if msgs[0].ID != "o100" {
		t.Errorf("unexpected text id: %q", msgs[0].ID)
	}
	if msgs[1].Role != protocol.RoleTool || msgs[1].Tool == nil {
		t.Fatalf("second message should be a tool row, got %+v", msgs[1])
	}
	if msgs[1].Tool.Name != "Shell" {
		t.Errorf("unexpected tool name: %q", msgs[1].Tool.Name)
	}
	// Description is preferred over the raw command for readability.
	if msgs[1].Tool.Summary != "Print current working directory" {
		t.Errorf("unexpected tool summary: %q", msgs[1].Tool.Summary)
	}
	if msgs[1].ID != "o100:tool1" {
		t.Errorf("unexpected tool id: %q", msgs[1].ID)
	}
	if msgs[2].Tool.Name != "Read" || msgs[2].Tool.Summary != "/tmp/main.go" {
		t.Errorf("unexpected second tool: %+v", msgs[2].Tool)
	}
	// Cursor records calls without outcomes; status must remain unknown.
	if msgs[1].Tool.Status != "" {
		t.Errorf("unexpected tool status: %q", msgs[1].Tool.Status)
	}
}

func TestCursorParseToolWithoutKnownInputKeys(t *testing.T) {
	p := NewCursorParser("cursor:s1")
	line := cursorLine(t, map[string]any{
		"role": "assistant",
		"message": map[string]any{"content": []any{
			map[string]any{"type": "tool_use", "name": "GetDynamicTools",
				"input": map[string]any{"namespace": "cursor", "toolName": "WebSearch"}},
		}},
	})
	msgs := p.Parse(line, 0)
	if len(msgs) != 1 || msgs[0].Tool == nil {
		t.Fatalf("expected 1 tool message, got %+v", msgs)
	}
	if msgs[0].Tool.Name != "GetDynamicTools" {
		t.Errorf("unexpected tool name: %q", msgs[0].Tool.Name)
	}
	if msgs[0].Tool.Summary != "" {
		t.Errorf("summary should be empty when it would repeat the name, got %q",
			msgs[0].Tool.Summary)
	}
}

func TestCursorParseTurnEnded(t *testing.T) {
	p := NewCursorParser("cursor:s1")
	if msgs := p.Parse(`{"type":"turn_ended","status":"success"}`, 0); len(msgs) != 0 {
		t.Errorf("successful turn end should map to nothing, got %+v", msgs)
	}
	msgs := p.Parse(`{"type":"turn_ended","status":"error","error":"User aborted request"}`, 7)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != protocol.RoleSystem {
		t.Errorf("expected system role, got %q", msgs[0].Role)
	}
	if msgs[0].Text != "Cursor turn failed: User aborted request" {
		t.Errorf("unexpected text: %q", msgs[0].Text)
	}
}

func TestCursorParseSkipsJunk(t *testing.T) {
	p := NewCursorParser("cursor:s1")
	for _, line := range []string{
		"",
		"not json",
		`{"role":"assistant"}`,
		`{"type":"something_else"}`,
		`{"role":"assistant","message":{"content":[{"type":"reasoning","text":"hmm"}]}}`,
	} {
		if msgs := p.Parse(line, 0); len(msgs) != 0 {
			t.Errorf("line %q should map to nothing, got %+v", line, msgs)
		}
	}
}

func TestCursorStateFromLine(t *testing.T) {
	cases := []struct {
		line  string
		state protocol.State
		ok    bool
	}{
		{`{"type":"turn_ended","status":"success"}`, protocol.StateIdle, true},
		{`{"type":"turn_ended","status":"error"}`, protocol.StateIdle, true},
		{`{"role":"user","message":{"content":[]}}`, protocol.StateBusy, true},
		{`{"role":"assistant","message":{"content":[]}}`, protocol.StateBusy, true},
		{`{"type":"something_else"}`, "", false},
		{"garbage", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		state, ok := CursorStateFromLine(tc.line)
		if state != tc.state || ok != tc.ok {
			t.Errorf("line %q: got (%q, %v), want (%q, %v)",
				tc.line, state, ok, tc.state, tc.ok)
		}
	}
}

func TestCursorSessionName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	content := cursorLine(t, map[string]any{
		"role": "user",
		"message": map[string]any{"content": textContent(
			"<timestamp>t</timestamp>\n<user_query>\nrefactor the auth module to use JWT tokens</user_query>")},
	}) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CursorSessionName(path); got != "refactor the auth module to use JWT tokens" {
		t.Errorf("unexpected name: %q", got)
	}
	if got := CursorSessionName(filepath.Join(dir, "missing.jsonl")); got != "" {
		t.Errorf("missing file should yield empty name, got %q", got)
	}
	other := filepath.Join(dir, "o.jsonl")
	if err := os.WriteFile(other, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CursorSessionName(other); got != "" {
		t.Errorf("non-user opening should yield empty name, got %q", got)
	}
}
