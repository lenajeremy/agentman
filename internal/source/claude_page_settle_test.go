package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Paging reads backwards, so a page meets a tool's result before its call.
// When the page filled up between the two, the call landed on the next page,
// whose fresh parser never saw the result, and showed as running for good.
// Parallel tool calls put a page boundary exactly there often: on this Mac,
// 45 finished tool rows across 525 transcripts paged in as running.
func TestAPageBoundaryBetweenACallAndItsResultLeavesNothingRunning(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "t.jsonl")
	lines := []string{
		`{"type":"assistant","uuid":"a1","timestamp":"2026-10-01T10:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_A","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-10-01T10:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_B","name":"Bash","input":{"command":"pwd"}}]}}`,
		`{"type":"user","uuid":"u1","timestamp":"2026-10-01T10:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_A","content":"a.txt"}]}}`,
		`{"type":"user","uuid":"u2","timestamp":"2026-10-01T10:00:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_B","content":"/work"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	const id = "claude:s1"
	src.sessions[id] = claudeSession{transcript: path}

	status := map[string]protocol.ToolStatus{}
	before := ""
	for range 4 {
		page, err := src.Page(context.Background(), id, before, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range page.Messages {
			if message.Tool != nil {
				status[message.ID] = message.Tool.Status
			}
		}
		if !page.HasMore {
			break
		}
		before = page.NextCursor
	}
	for _, call := range []string{"toolu_A", "toolu_B"} {
		if status[call] != protocol.ToolOK {
			t.Errorf("%s paged in as %q, want ok", call, status[call])
		}
	}
}
