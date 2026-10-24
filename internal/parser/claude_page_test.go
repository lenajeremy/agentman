package parser_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/jsonl"
	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// A screenshot an agent read is stored whole in its transcript. That one
// result spent a page's byte budget, the page ended before the call, and the
// next page showed the call running forever. Four read at once are worse.
func TestACallWhoseResultsFillThePageStillSettles(t *testing.T) {
	line := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	image := strings.Repeat("A", 3<<20)
	call := map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{"file_path": "/tmp/shot.png"}}
	var uses []any
	for _, id := range []string{"c1", "c2", "c3", "c4"} {
		block := map[string]any{"id": id}
		for k, v := range call {
			block[k] = v
		}
		uses = append(uses, block)
	}
	lines := []string{
		line(map[string]any{"type": "user", "uuid": "u0", "timestamp": "2026-10-24T18:00:00Z",
			"message": map[string]any{"role": "user", "content": "look at these"}}),
		line(map[string]any{"type": "assistant", "uuid": "a1", "timestamp": "2026-10-24T18:00:01Z",
			"message": map[string]any{"role": "assistant", "content": uses}}),
	}
	for i, id := range []string{"c1", "c2", "c3", "c4"} {
		lines = append(lines, line(map[string]any{"type": "user", "uuid": "r" + id, "timestamp": "2026-10-24T18:00:02Z",
			"message": map[string]any{"role": "user", "content": []any{map[string]any{
				"type": "tool_result", "tool_use_id": id,
				"content": []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": image}}},
			}}}}))
		_ = i
	}
	lines = append(lines, line(map[string]any{"type": "assistant", "uuid": "a2", "timestamp": "2026-10-24T18:00:03Z",
		"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Both look fine."}}}}))
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Page as the app does, newest first, a fresh parser per page.
	var messages []protocol.Message
	var before *int64
	for page := 0; page < 4; page++ {
		p := parser.NewClaudeParser("claude:s")
		result, err := jsonl.CollectBackward(path, jsonl.BackwardOptions{
			Want: 1, Map: p.Parse, Unsettled: p.AwaitingCalls, Before: before,
		})
		if err != nil {
			t.Fatal(err)
		}
		messages = append(messages, result.Messages...)
		if !result.HasMore {
			break
		}
		cursor := result.NextCursor
		before = &cursor
	}
	settled := 0
	for _, m := range messages {
		if m.Tool == nil {
			continue
		}
		if m.Tool.Status != protocol.ToolOK {
			t.Errorf("%s is %s on the page that holds its result", m.ID, m.Tool.Status)
		}
		settled++
	}
	if settled != 4 {
		t.Fatalf("%d of the 4 calls were shown; a call that opens a page without its result shows as running", settled)
	}
}
