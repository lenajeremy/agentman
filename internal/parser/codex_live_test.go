package parser

import (
	"encoding/json"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func liveItem(t *testing.T, item obj) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func flushOne(t *testing.T, live *CodexLive) protocol.Message {
	t.Helper()
	rows := live.Flush()
	if len(rows) != 1 {
		t.Fatalf("flushed %+v, want one row", rows)
	}
	return rows[0]
}

func flushNone(t *testing.T, live *CodexLive) {
	t.Helper()
	if rows := live.Flush(); len(rows) != 0 {
		t.Fatalf("flushed %+v, want nothing", rows)
	}
}

func TestCodexLiveStreamsAReplyUnderItsItemID(t *testing.T) {
	live := NewCodexLive("codex:s")
	live.Started(liveItem(t, obj{"type": "agentMessage", "id": "msg_1", "text": ""}), 1000)
	flushNone(t, live) // no words yet

	live.Delta("msg_1", "The", 1001)
	live.Delta("msg_1", " kettle", 1002)
	row := flushOne(t, live)
	if row.ID != "msg_1" || row.Role != protocol.RoleAssistant || row.Text != "The kettle" || row.Ts != 1000 || row.SessionID != "codex:s" {
		t.Fatalf("row = %+v", row)
	}
	flushNone(t, live) // unchanged since

	live.Delta("msg_1", " hums", 1003)
	if row := flushOne(t, live); row.Text != "The kettle hums" {
		t.Fatalf("text = %q", row.Text)
	}

	live.Completed(liveItem(t, obj{"type": "agentMessage", "id": "msg_1", "text": "The kettle hums against the rain."}))
	if row := flushOne(t, live); row.Text != "The kettle hums against the rain." {
		t.Fatalf("final text = %q", row.Text)
	}

	// The rollout delivered it: a late delta must not overwrite its row.
	live.Seen([]protocol.Message{{ID: "msg_1"}})
	live.Delta("msg_1", " late", 1004)
	live.Completed(liveItem(t, obj{"type": "agentMessage", "id": "msg_1", "text": "other"}))
	flushNone(t, live)
}

// Joined in the middle of a reply: the start was before the join.
func TestCodexLiveCatchesAReplyAlreadyUnderWay(t *testing.T) {
	live := NewCodexLive("codex:s")
	live.Delta("msg_1", "half", 5)
	if row := flushOne(t, live); row.ID != "msg_1" || row.Role != protocol.RoleAssistant || row.Text != "half" {
		t.Fatalf("row = %+v", row)
	}
}

func TestCodexLiveFormatsCitationsAsTheRolloutDoes(t *testing.T) {
	text := "See :codex-file-citation{path=/repo/a.go line_start=1 line_end=2} for it."
	live := NewCodexLive("codex:s")
	live.Delta("msg_1", text, 1)
	want := codexCitations(text)
	if got := flushOne(t, live).Text; got != want {
		t.Fatalf("text = %q, want the rollout's %q", got, want)
	}
}

// A command shows from the moment it starts, as the row the rollout will
// make once it finishes: same id, same name, same summary.
func TestCodexLiveShowsACommandAsTheRolloutWill(t *testing.T) {
	live := NewCodexLive("codex:s")
	live.Started(liveItem(t, obj{
		"type": "commandExecution", "id": "exec-1", "command": "/bin/zsh -lc 'echo hi > note.txt'",
		"commandActions": []any{obj{"type": "unknown", "command": "echo hi > note.txt"}},
		"status":         "inProgress", "aggregatedOutput": nil, "exitCode": nil,
	}), 2000)
	running := flushOne(t, live)
	if running.Role != protocol.RoleTool || running.Tool.Status != protocol.ToolRunning {
		t.Fatalf("running = %+v %+v", running, running.Tool)
	}

	live.Delta("exec-1", "line one\n", 2001)
	if row := flushOne(t, live); row.Text != "line one" {
		t.Fatalf("output so far = %q", row.Text)
	}

	live.Completed(liveItem(t, obj{
		"type": "commandExecution", "id": "exec-1", "command": "/bin/zsh -lc 'echo hi > note.txt'",
		"commandActions": []any{obj{"type": "unknown", "command": "echo hi > note.txt"}},
		"status":         "completed", "aggregatedOutput": "line one\nline two\n", "exitCode": 0,
	}))
	done := flushOne(t, live)

	finished := onlyRow(t, codexEvent(obj{
		"type": "CommandExecution", "id": "exec-1", "status": "completed", "exit_code": 0,
		"command": []string{"/bin/zsh", "-lc", "echo hi > note.txt"}, "aggregated_output": "line one\nline two\n",
		"parsed_cmd": []any{obj{"type": "unknown", "cmd": "echo hi > note.txt"}},
	}))
	if done.ID != finished.ID || done.Text != finished.Text || *done.Tool != *finished.Tool {
		t.Fatalf("live row %+v %+v\nrollout row %+v %+v", done, *done.Tool, finished, *finished.Tool)
	}
	if running.Tool.Name != finished.Tool.Name || running.Tool.Summary != finished.Tool.Summary {
		t.Fatalf("the running row (%+v) does not read as the finished one (%+v)", *running.Tool, *finished.Tool)
	}
}

func TestCodexLiveMarksAFailedCommand(t *testing.T) {
	live := NewCodexLive("codex:s")
	live.Started(liveItem(t, obj{"type": "commandExecution", "id": "exec-1", "command": "false"}), 1)
	live.Flush()
	live.Completed(liveItem(t, obj{"type": "commandExecution", "id": "exec-1", "command": "false", "status": "failed", "exitCode": 1, "aggregatedOutput": ""}))
	if row := flushOne(t, live); row.Tool.Status != protocol.ToolError {
		t.Fatalf("status = %s", row.Tool.Status)
	}
	live.Started(liveItem(t, obj{"type": "commandExecution", "id": "exec-2", "command": "rm -rf build"}), 2)
	live.Flush()
	live.Completed(liveItem(t, obj{"type": "commandExecution", "id": "exec-2", "command": "rm -rf build", "status": "declined"}))
	if row := flushOne(t, live); row.Tool.Status != protocol.ToolError {
		t.Fatalf("declined status = %s", row.Tool.Status)
	}
}

func TestCodexLiveNamesAnEditAsTheRolloutWill(t *testing.T) {
	live := NewCodexLive("codex:s")
	live.Started(liveItem(t, obj{"type": "fileChange", "id": "exec-9", "status": "inProgress", "changes": []any{
		obj{"path": "/repo/old.go", "kind": obj{"type": "delete"}, "diff": ""},
		obj{"path": "/repo/b.go", "kind": obj{"type": "update"}, "diff": "@@"},
		obj{"path": "/repo/a.md", "kind": obj{"type": "add"}, "diff": "# T"},
	}}), 1)
	running := flushOne(t, live)
	finished := onlyRow(t, codexEvent(obj{"type": "FileChange", "id": "exec-9", "status": "completed", "changes": obj{
		"/repo/b.go":   obj{"type": "update", "unified_diff": "@@ -1 +1 @@\n-a\n+b\n"},
		"/repo/a.md":   obj{"type": "add", "content": "# T\n"},
		"/repo/old.go": obj{"type": "delete", "content": "package old\n"},
	}}))
	if running.ID != finished.ID || running.Tool.Name != finished.Tool.Name || running.Tool.Summary != finished.Tool.Summary {
		t.Fatalf("running %+v, finished %+v", *running.Tool, *finished.Tool)
	}
	if running.Tool.Status != protocol.ToolRunning {
		t.Fatalf("status = %s", running.Tool.Status)
	}
}

func TestCodexLiveNamesAnMCPCallAsTheRolloutWill(t *testing.T) {
	live := NewCodexLive("codex:s")
	live.Started(liveItem(t, obj{
		"type": "mcpToolCall", "id": "exec-3", "server": "docs", "tool": "search", "status": "inProgress",
		"arguments": obj{"query": "tmux send-keys"},
	}), 1)
	running := flushOne(t, live)
	finished := onlyRow(t, codexEvent(obj{
		"type": "McpToolCall", "id": "exec-3", "server": "docs", "tool": "search", "status": "completed",
		"arguments": obj{"query": "tmux send-keys"},
		"result":    obj{"content": []any{obj{"type": "text", "text": "3 results"}}},
	}))
	if running.Tool.Name != finished.Tool.Name || running.Tool.Summary != finished.Tool.Summary || running.Tool.Title != finished.Tool.Title {
		t.Fatalf("running %+v, finished %+v", *running.Tool, *finished.Tool)
	}
}

func TestCodexLiveLeavesReasoningAndUnknownItemsToTheRollout(t *testing.T) {
	live := NewCodexLive("codex:s")
	live.Started(liveItem(t, obj{"type": "reasoning", "id": "rs_1"}), 1)
	live.Started(liveItem(t, obj{"type": "webSearch", "id": "ws_1", "query": "x"}), 2)
	live.Started(liveItem(t, obj{"type": "userMessage", "id": "u1", "content": []any{}}), 3)
	flushNone(t, live)
}

// The rollout can be quicker than the daemon's start event to reach a
// follow: once it has, the start is too late to show.
func TestCodexLiveIgnoresAnItemTheRolloutAlreadyDelivered(t *testing.T) {
	live := NewCodexLive("codex:s")
	live.Seen([]protocol.Message{{ID: "exec-1"}})
	live.Started(liveItem(t, obj{"type": "commandExecution", "id": "exec-1", "command": "ls"}), 1)
	live.Delta("exec-1", "out", 2)
	flushNone(t, live)
}

func TestCodexLiveFlushesInStartOrder(t *testing.T) {
	live := NewCodexLive("codex:s")
	live.Started(liveItem(t, obj{"type": "commandExecution", "id": "exec-1", "command": "ls"}), 1)
	live.Started(liveItem(t, obj{"type": "agentMessage", "id": "msg_2"}), 2)
	live.Delta("msg_2", "done", 3)
	rows := live.Flush()
	if len(rows) != 2 || rows[0].ID != "exec-1" || rows[1].ID != "msg_2" {
		t.Fatalf("rows = %+v", rows)
	}
}
