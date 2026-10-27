package parser

import (
	"testing"

	"github.com/lenajeremy/agentman/internal/jsonl"
	"github.com/lenajeremy/agentman/internal/protocol"
)

func claudeEditResult(uuid, toolUseID string, result any) obj {
	record := claudeToolResult(uuid, toolUseID, "The file /repo/a.go has been updated successfully.", false)
	record["toolUseResult"] = result
	return record
}

// An edit shows the change it made, as a diff the apps colour, read the way
// a thread is read: backwards a page at a time, and forwards as it happens.
func TestClaudeEditShowsItsChange(t *testing.T) {
	path := writeFixture(t, []any{
		claudeAssistant("a1", []any{
			obj{"type": "tool_use", "id": "toolu_1", "name": "Edit", "input": obj{"file_path": "/repo/a.go", "old_string": "b", "new_string": "c"}},
		}),
		claudeEditResult("u1", "toolu_1", obj{
			"filePath": "/repo/a.go", "oldString": "b", "newString": "c",
			"structuredPatch": []any{obj{"oldStart": 1, "oldLines": 2, "newStart": 1, "newLines": 2, "lines": []any{" a", "-b", "+c"}}},
		}),
	})
	want := "--- a/repo/a.go\n+++ b/repo/a.go\n@@ -1,2 +1,2 @@\n a\n-b\n+c"

	backward := pageAll(t, path, NewClaudeParser("claude:s"), 10)
	if len(backward) != 1 || backward[0].Text != want || backward[0].Tool.Status != protocol.ToolOK {
		t.Fatalf("backward = %+v", backward)
	}

	p := NewClaudeParser("claude:s")
	lines, err := jsonl.NewTail(path).Read()
	if err != nil {
		t.Fatal(err)
	}
	var forward []protocol.Message
	for _, line := range lines {
		forward = append(forward, p.Parse(line.Text, line.Offset)...)
	}
	if len(forward) != 2 || forward[1].Text != want || forward[1].Tool.Summary != "/repo/a.go" {
		t.Fatalf("forward = %+v", forward)
	}
}

func TestClaudeNewFileShowsItsText(t *testing.T) {
	path := writeFixture(t, []any{
		claudeAssistant("a1", []any{
			obj{"type": "tool_use", "id": "toolu_1", "name": "Write", "input": obj{"file_path": "/repo/new.md", "content": "# Hi\n"}},
		}),
		claudeEditResult("u1", "toolu_1", obj{"type": "create", "filePath": "/repo/new.md", "content": "# Hi\nthere\n", "structuredPatch": []any{}}),
	})
	msgs := pageAll(t, path, NewClaudeParser("claude:s"), 10)
	if len(msgs) != 1 || msgs[0].Text != "--- /dev/null\n+++ b/repo/new.md\n@@ -0,0 +1,2 @@\n+# Hi\n+there" {
		t.Fatalf("rows = %+v", msgs)
	}
}

// A failed edit says why, and a result that is not a file change keeps its
// words.
func TestClaudeEditFailuresAndOtherResultsKeepTheirText(t *testing.T) {
	failed := claudeToolResult("u1", "toolu_1", "String to replace not found in file.", true)
	failed["toolUseResult"] = "Error: String to replace not found in file."
	read := claudeToolResult("u2", "toolu_2", "1\tpackage a", false)
	read["toolUseResult"] = obj{"type": "text", "file": obj{"filePath": "/repo/a.go", "content": "package a"}}
	path := writeFixture(t, []any{
		claudeAssistant("a1", []any{
			obj{"type": "tool_use", "id": "toolu_1", "name": "Edit", "input": obj{"file_path": "/repo/a.go"}},
			obj{"type": "tool_use", "id": "toolu_2", "name": "Read", "input": obj{"file_path": "/repo/a.go"}},
		}),
		failed, read,
	})
	msgs := pageAll(t, path, NewClaudeParser("claude:s"), 10)
	if len(msgs) != 2 || msgs[0].Text != "String to replace not found in file." || msgs[0].Tool.Status != protocol.ToolError ||
		msgs[1].Text != "1\tpackage a" {
		t.Fatalf("rows = %+v", msgs)
	}
}
