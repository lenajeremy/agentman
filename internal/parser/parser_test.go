package parser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/jsonl"
	"github.com/lenajeremy/agentman/internal/protocol"
)

func writeFixture(t *testing.T, records []any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.jsonl")
	var b strings.Builder
	for _, rec := range records {
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// pageAll reads a fixture back through the real backward reader, which is how
// these parsers are actually driven when the user scrolls.
func pageAll(t *testing.T, path string, p Parser, want int) []protocol.Message {
	t.Helper()
	got, err := jsonl.CollectBackward(path, jsonl.BackwardOptions{Want: want, Map: p.Parse})
	if err != nil {
		t.Fatal(err)
	}
	return got.Messages
}

type obj = map[string]any

/* --------------------------------- Claude -------------------------------- */

func claudeUser(uuid, text string) obj {
	return obj{
		"type": "user", "uuid": uuid, "timestamp": "2026-08-10T10:00:00.000Z",
		"message": obj{"role": "user", "content": text},
	}
}

func claudeAssistant(uuid string, content []any) obj {
	return obj{
		"type": "assistant", "uuid": uuid, "timestamp": "2026-08-10T10:00:01.000Z",
		"message": obj{"role": "assistant", "content": content},
	}
}

func claudeToolResult(uuid, toolUseID string, content any, isError bool) obj {
	return obj{
		"type": "user", "uuid": uuid, "timestamp": "2026-08-10T10:00:02.000Z",
		"message": obj{"role": "user", "content": []any{
			obj{"type": "tool_result", "tool_use_id": toolUseID, "content": content, "is_error": isError},
		}},
	}
}

func TestClaudeTurnExpandsToTextAndToolRows(t *testing.T) {
	path := writeFixture(t, []any{
		claudeUser("u1", "run the tests"),
		claudeAssistant("a1", []any{
			obj{"type": "text", "text": "Running them now."},
			obj{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": obj{"command": "npm test"}},
		}),
		claudeToolResult("u2", "toolu_1", "7 passing", false),
	})

	msgs := pageAll(t, path, NewClaudeParser("claude:s"), 20)

	if len(msgs) != 3 {
		t.Fatalf("a tool call and its result must collapse into one row; got %d messages", len(msgs))
	}
	if msgs[0].Role != protocol.RoleUser || msgs[0].Text != "run the tests" {
		t.Errorf("unexpected first message: %+v", msgs[0])
	}
	if msgs[1].Role != protocol.RoleAssistant || msgs[1].Text != "Running them now." {
		t.Errorf("unexpected second message: %+v", msgs[1])
	}
	if msgs[2].Role != protocol.RoleTool || msgs[2].Tool.Name != "Bash" {
		t.Fatalf("unexpected third message: %+v", msgs[2])
	}
	if msgs[2].Tool.Summary != "npm test" {
		t.Errorf("summary = %q, want the command", msgs[2].Tool.Summary)
	}
	if msgs[2].Tool.Status != protocol.ToolOK {
		t.Errorf("status = %q, want ok", msgs[2].Tool.Status)
	}
	if msgs[2].Text != "7 passing" {
		t.Errorf("result preview = %q", msgs[2].Text)
	}
}

func TestClaudeFailedToolsAreMarkedErrors(t *testing.T) {
	path := writeFixture(t, []any{
		claudeAssistant("a1", []any{
			obj{"type": "tool_use", "id": "toolu_9", "name": "Bash", "input": obj{"command": "false"}},
		}),
		claudeToolResult("u2", "toolu_9", "Permission to use Bash has been denied", true),
	})

	msgs := pageAll(t, path, NewClaudeParser("claude:s"), 20)

	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].Tool.Status != protocol.ToolError {
		t.Errorf("status = %q, want error", msgs[0].Tool.Status)
	}
}

func TestClaudeLiveTailSettlesRunningToolUnderSameID(t *testing.T) {
	// Forwards we meet the call before its result, so the row is emitted twice:
	// once running, once settled. Sharing an ID is what lets the app upsert
	// rather than show the tool twice.
	path := writeFixture(t, []any{
		claudeAssistant("a1", []any{
			obj{"type": "tool_use", "id": "toolu_5", "name": "Read", "input": obj{"file_path": "/tmp/x.ts"}},
		}),
		claudeToolResult("u2", "toolu_5", "contents", false),
	})

	p := NewClaudeParser("claude:s")
	tail := jsonl.NewTail(path)
	lines, err := tail.Read()
	if err != nil {
		t.Fatal(err)
	}
	var emitted []protocol.Message
	for _, line := range lines {
		emitted = append(emitted, p.Parse(line.Text, line.Offset)...)
	}

	if len(emitted) != 2 {
		t.Fatalf("got %d messages, want a running row then a settled one", len(emitted))
	}
	if emitted[0].ID != emitted[1].ID {
		t.Errorf("both rows must share the tool_use id: %q vs %q", emitted[0].ID, emitted[1].ID)
	}
	if emitted[0].Tool.Status != protocol.ToolRunning {
		t.Errorf("first status = %q, want running", emitted[0].Tool.Status)
	}
	if emitted[1].Tool.Status != protocol.ToolOK {
		t.Errorf("second status = %q, want ok", emitted[1].Tool.Status)
	}
	if emitted[0].Tool.Summary != "/tmp/x.ts" {
		t.Errorf("summary = %q, want the file path", emitted[0].Tool.Summary)
	}
}

func TestClaudeSettledToolKeepsItsCommand(t *testing.T) {
	// Regression: the row emitted when a tool finished carried only the name
	// and status, and because it replaces the running row by id, the command
	// was erased. On screen that read as tool rows showing a bare "Bash" or
	// "Read" seemingly at random — the difference being only whether the tool
	// had completed yet.
	path := writeFixture(t, []any{
		claudeAssistant("a1", []any{
			obj{"type": "tool_use", "id": "toolu_7", "name": "Bash",
				"input": obj{"command": "go test ./..."}},
		}),
		claudeToolResult("u2", "toolu_7", "ok", false),
	})

	msgs := pageAll(t, path, NewClaudeParser("claude:s"), 20)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1 settled row", len(msgs))
	}
	if msgs[0].Tool.Summary != "go test ./..." {
		t.Errorf("Summary = %q, want the command to survive completion", msgs[0].Tool.Summary)
	}

	// The same must hold reading forwards, where the settled row is a genuine
	// second emission rather than a merge.
	p := NewClaudeParser("claude:s")
	tail := jsonl.NewTail(path)
	lines, err := tail.Read()
	if err != nil {
		t.Fatal(err)
	}
	var emitted []protocol.Message
	for _, line := range lines {
		emitted = append(emitted, p.Parse(line.Text, line.Offset)...)
	}
	if len(emitted) != 2 {
		t.Fatalf("got %d messages, want a running row then a settled one", len(emitted))
	}
	if emitted[1].Tool.Summary != "go test ./..." {
		t.Errorf("settled row lost the command: %+v", emitted[1].Tool)
	}
}

func TestClaudeBookkeepingAndThinkingStayOut(t *testing.T) {
	meta := claudeUser("u0", "hi")
	meta["isMeta"] = true

	path := writeFixture(t, []any{
		obj{"type": "mode", "mode": "normal"},
		obj{"type": "permission-mode", "permissionMode": "auto"},
		obj{"type": "ai-title", "title": "Something"},
		obj{"type": "file-history-snapshot", "messageId": "m"},
		obj{"type": "system", "uuid": "s1", "content": "<command-name>/resume</command-name>"},
		meta,
		claudeAssistant("a1", []any{
			obj{"type": "thinking", "thinking": "long internal monologue", "signature": "abc"},
			obj{"type": "text", "text": "Hello."},
		}),
	})

	msgs := pageAll(t, path, NewClaudeParser("claude:s"), 20)

	if len(msgs) != 1 || msgs[0].Text != "Hello." {
		t.Fatalf("only the visible text should survive, got %+v", msgs)
	}
}

func TestClaudeSubagentOutputIsFlagged(t *testing.T) {
	rec := claudeAssistant("a1", []any{obj{"type": "text", "text": "from a subagent"}})
	rec["isSidechain"] = true
	path := writeFixture(t, []any{rec})

	msgs := pageAll(t, path, NewClaudeParser("claude:s"), 5)

	if len(msgs) != 1 || !msgs[0].IsSidechain {
		t.Fatalf("subagent output must be flagged, got %+v", msgs)
	}
}

func TestIsKnownClaudeRecordType(t *testing.T) {
	for _, known := range []string{"user", "assistant", "mode", "ai-title", "file-history-snapshot"} {
		if !IsKnownClaudeRecordType(known) {
			t.Errorf("%q should be recognized", known)
		}
	}
	if IsKnownClaudeRecordType("some-future-record") {
		t.Error("an unrecognized type must be reported so doctor can flag format drift")
	}
}

/* --------------------------------- Codex --------------------------------- */

func codexEvent(item any) obj {
	return obj{
		"timestamp": "2026-08-10T10:00:00.000Z",
		"type":      "event_msg",
		"payload":   obj{"type": "item_completed", "item": item},
	}
}

func TestCodexIgnoresDuplicateHistoryStream(t *testing.T) {
	path := writeFixture(t, []any{
		// The same assistant turn appears in both streams; only one may surface.
		obj{
			"timestamp": "2026-08-10T10:00:00.000Z", "type": "response_item",
			"payload": obj{"type": "message", "role": "assistant",
				"content": []any{obj{"type": "output_text", "text": "hi"}}},
		},
		codexEvent(obj{"type": "AgentMessage", "id": "msg_1",
			"content": []any{obj{"type": "Text", "text": "hi"}}}),
		// "developer" records are injected instructions the user never wrote.
		obj{
			"timestamp": "2026-08-10T10:00:00.000Z", "type": "response_item",
			"payload": obj{"type": "message", "role": "developer",
				"content": []any{obj{"type": "input_text", "text": "<skills>"}}},
		},
	})

	msgs := pageAll(t, path, NewCodexParser("codex:s"), 20)

	if len(msgs) != 1 {
		t.Fatalf("the turn must appear exactly once, got %d messages", len(msgs))
	}
	if msgs[0].Role != protocol.RoleAssistant || msgs[0].Text != "hi" {
		t.Errorf("unexpected message: %+v", msgs[0])
	}
}

func TestCodexCommandsUseParsedFormAndExitStatus(t *testing.T) {
	path := writeFixture(t, []any{
		codexEvent(obj{
			"type": "CommandExecution", "id": "exec-1",
			"command":    []any{"/bin/zsh", "-lc", "go test ./..."},
			"parsed_cmd": []any{obj{"type": "run", "cmd": "go test ./..."}},
			"status":     "completed", "exit_code": 0,
			"aggregated_output": "ok  all tests pass",
		}),
		codexEvent(obj{
			"type": "CommandExecution", "id": "exec-2",
			"command":    []any{"/bin/zsh", "-lc", "false"},
			"parsed_cmd": []any{obj{"type": "run", "cmd": "false"}},
			"status":     "completed", "exit_code": 1,
		}),
	})

	msgs := pageAll(t, path, NewCodexParser("codex:s"), 20)

	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].Tool.Summary != "go test ./..." {
		t.Errorf("summary = %q, want parsed_cmd rather than the shell wrapper", msgs[0].Tool.Summary)
	}
	if msgs[0].Tool.Status != protocol.ToolOK {
		t.Errorf("status = %q, want ok", msgs[0].Tool.Status)
	}
	if msgs[1].Tool.Status != protocol.ToolError {
		t.Errorf("a non-zero exit must read as a failure, got %q", msgs[1].Tool.Status)
	}
}

func TestCodexFileChangeSummarizesPaths(t *testing.T) {
	path := writeFixture(t, []any{
		codexEvent(obj{"type": "FileChange", "id": "fc-1", "changes": obj{
			"/repo/tasks.md": obj{"type": "update", "unified_diff": "@@"},
		}}),
	})

	msgs := pageAll(t, path, NewCodexParser("codex:s"), 5)

	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].Tool.Name != "Edit" || msgs[0].Tool.Summary != "/repo/tasks.md" {
		t.Errorf("unexpected file change row: %+v", msgs[0].Tool)
	}
}

func TestCodexMultiFileChangeSummaryIsStable(t *testing.T) {
	// Go randomizes map iteration, so an unsorted summary would differ between
	// reads and make the same message look like it changed.
	path := writeFixture(t, []any{
		codexEvent(obj{"type": "FileChange", "id": "fc-2", "changes": obj{
			"/repo/a.go": obj{"type": "update"},
			"/repo/b.go": obj{"type": "update"},
			"/repo/c.go": obj{"type": "update"},
			"/repo/d.go": obj{"type": "update"},
		}}),
	})

	first := pageAll(t, path, NewCodexParser("codex:s"), 5)[0].Tool.Summary
	for range 8 {
		if got := pageAll(t, path, NewCodexParser("codex:s"), 5)[0].Tool.Summary; got != first {
			t.Fatalf("summary is unstable across reads: %q vs %q", got, first)
		}
	}
	if !strings.HasPrefix(first, "4 files: a.go, b.go, c.go") {
		t.Errorf("unexpected summary: %q", first)
	}
}

func TestCodexTurnBoundariesDriveState(t *testing.T) {
	cases := []struct {
		payloadType string
		want        protocol.State
		ok          bool
	}{
		{"task_started", protocol.StateBusy, true},
		{"task_complete", protocol.StateIdle, true},
		{"turn_aborted", protocol.StateIdle, true},
		{"token_count", "", false},
	}
	for _, tc := range cases {
		line := fmt.Sprintf(`{"type":"event_msg","payload":{"type":%q}}`, tc.payloadType)
		got, ok := CodexStateFromLine(line)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", tc.payloadType, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParsersSurviveMalformedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte("{\"type\":\"user\"\nnot json at all\n{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if msgs := pageAll(t, path, NewClaudeParser("claude:s"), 5); len(msgs) != 0 {
		t.Errorf("claude parser emitted %d messages from junk", len(msgs))
	}
	if msgs := pageAll(t, path, NewCodexParser("codex:s"), 5); len(msgs) != 0 {
		t.Errorf("codex parser emitted %d messages from junk", len(msgs))
	}
}

func TestClipTruncatesOnRuneBoundary(t *testing.T) {
	// Byte-slicing multi-byte text would emit replacement characters.
	got := clip(strings.Repeat("日", 300), 10)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected an ellipsis, got %q", got)
	}
	for _, r := range got {
		if r == '�' {
			t.Fatalf("clip corrupted a multi-byte rune: %q", got)
		}
	}
}

func TestClipBlockKeepsLines(t *testing.T) {
	out := ClipBlock("one\ntwo\nthree", 10, 100)
	if out != "one\ntwo\nthree" {
		t.Fatalf("lines were flattened: %q", out)
	}
}

func TestClipBlockNormalizesEdges(t *testing.T) {
	out := ClipBlock("\r\n\r\n  a  \r\nb\t\n\n\n", 10, 100)
	if out != "  a\nb" {
		t.Fatalf("edges not trimmed: %q", out)
	}
}

func TestClipBlockCountsDroppedLines(t *testing.T) {
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, "line")
	}
	out := ClipBlock(strings.Join(lines, "\n"), 10, 10000)
	if !strings.HasSuffix(out, "\n… 40 more lines") {
		t.Fatalf("missing drop marker: %q", out)
	}
	if got := strings.Count(out, "\n"); got != 10 {
		t.Fatalf("kept %d breaks, want 10", got)
	}
}

func TestClipBlockSingleDroppedLineReadsAsOne(t *testing.T) {
	out := ClipBlock("a\nb\nc", 2, 10000)
	if !strings.HasSuffix(out, "\n… 1 more line") {
		t.Fatalf("plural for one line: %q", out)
	}
}

// The character cap must not leave a half-finished line pretending to be whole.
func TestClipBlockDropsThePartialTrailingLine(t *testing.T) {
	out := ClipBlock("aaaa\nbbbb\ncccc", 10, 7)
	if !strings.HasPrefix(out, "aaaa\n…") {
		t.Fatalf("partial line survived: %q", out)
	}
	if strings.Contains(out, "bb") {
		t.Fatalf("partial line survived: %q", out)
	}
}

// A single line longer than the budget has no whole line to fall back to, so
// it is truncated in place and marked.
func TestClipBlockTruncatesALoneLongLine(t *testing.T) {
	out := ClipBlock(strings.Repeat("x", 100), 10, 20)
	if out != strings.Repeat("x", 20)+"…" {
		t.Fatalf("lone line clipped wrong: %q", out)
	}
}

func TestClipBlockCountsRunesNotBytes(t *testing.T) {
	out := ClipBlock(strings.Repeat("é", 10), 10, 10)
	if out != strings.Repeat("é", 10) {
		t.Fatalf("multibyte text was clipped early: %q", out)
	}
}

func TestClipBlockEmpty(t *testing.T) {
	if out := ClipBlock("  \n\n ", 10, 100); out != "" {
		t.Fatalf("blank input produced %q", out)
	}
}

func TestClaudeToolOutputKeepsItsLines(t *testing.T) {
	// Regression: results went through clip, which joins strings.Fields with a
	// space. Grep hits, numbered listings and stack traces all arrived on the
	// phone as one reflowed monospace paragraph — the least readable form of
	// the most structured thing an agent produces.
	path := writeFixture(t, []any{
		claudeAssistant("a1", []any{
			obj{"type": "tool_use", "id": "toolu_1", "name": "Grep",
				"input": obj{"pattern": "presentLocalOrigin"}},
		}),
		claudeToolResult("u2", "toolu_1", "internal/relay/tunnel.go\ninternal/relay/tunnel_test.go", false),
	})

	msgs := pageAll(t, path, NewClaudeParser("claude:s"), 20)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if got := msgs[0].Text; got != "internal/relay/tunnel.go\ninternal/relay/tunnel_test.go" {
		t.Errorf("Text = %q, want the two hits on their own lines", got)
	}
}

func TestClaudeCommandKeepsItsLines(t *testing.T) {
	// A heredoc'd script flattened to one line is unreadable, and the row only
	// ever shows the first line anyway.
	script := "python3 - <<'PY'\nprint(1)\nPY"
	path := writeFixture(t, []any{
		claudeAssistant("a1", []any{
			obj{"type": "tool_use", "id": "toolu_1", "name": "Bash",
				"input": obj{"command": script}},
		}),
	})

	msgs := pageAll(t, path, NewClaudeParser("claude:s"), 20)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].Tool.Summary != script {
		t.Errorf("Summary = %q, want the script intact", msgs[0].Tool.Summary)
	}
}

func TestClaudePathSummaryStaysOneLine(t *testing.T) {
	path := writeFixture(t, []any{
		claudeAssistant("a1", []any{
			obj{"type": "tool_use", "id": "toolu_1", "name": "Read",
				"input": obj{"file_path": "/repo/internal/relay/tunnel.go"}},
		}),
	})

	msgs := pageAll(t, path, NewClaudeParser("claude:s"), 20)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].Tool.Summary != "/repo/internal/relay/tunnel.go" {
		t.Errorf("Summary = %q", msgs[0].Tool.Summary)
	}
}

func TestClipBlockDropsAnsiEscapes(t *testing.T) {
	out := ClipBlock("\x1b[0;32mPASS\x1b[0m ok\n\x1b]0;title\x07next", 10, 1000)
	if out != "PASS ok\nnext" {
		t.Fatalf("escapes survived: %q", out)
	}
}

func TestClipBlockHonoursCarriageReturnOverwrites(t *testing.T) {
	// A progress bar rewrites one line; only its last state was ever visible.
	out := ClipBlock("10%\r50%\r100%\ndone", 10, 1000)
	if out != "100%\ndone" {
		t.Fatalf("overwrites became lines: %q", out)
	}
}
