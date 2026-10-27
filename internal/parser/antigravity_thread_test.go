package parser

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func agyPrompt(t *testing.T, index int, request string) string {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"step_index": index, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE",
		"created_at": "2026-10-27T11:00:00Z",
		"content":    "<USER_REQUEST>\n" + request + "\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-10-27T11:00:00+00:00.\n</ADDITIONAL_METADATA>",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

const agyUploaded = "/Users/me/.gemini/antigravity-cli/brain/ef116096-84c3-4723-bbc3-aed0f3cca68b/.user_uploaded/att3.png"

// A picture sent to agy is a path at the start of the request, in the
// conversation's own upload folder; the thread shows it as the picture.
func TestAntigravityPicturesSentShowAsPictures(t *testing.T) {
	cases := []struct {
		request string
		images  []string
		text    string
	}{
		{agyUploaded + " what colour is this? One word.", []string{agyUploaded}, "what colour is this? One word."},
		{"@" + agyUploaded + " what colour?", []string{agyUploaded}, "what colour?"},
		{agyUploaded, []string{agyUploaded}, ""},
		{agyUploaded + " " + "/Users/me/.agentman/images/antigravity-s/b.jpg\ncompare these",
			[]string{agyUploaded, "/Users/me/.agentman/images/antigravity-s/b.jpg"}, "compare these"},
	}
	for _, c := range cases {
		got := parseAntigravity(agyPrompt(t, 4, c.request))
		if len(got) != 1 || got[0].Role != protocol.RoleUser || got[0].Text != c.text || !slices.Equal(got[0].Images, c.images) {
			t.Errorf("%q: %+v", c.request, got)
		}
	}
}

// Only agy's upload folder and Agentman's own: a path someone typed stays
// words.
func TestAntigravityOtherPathsStayWords(t *testing.T) {
	for _, request := range []string{
		"/Users/me/Desktop/shot.png what is wrong here?",
		"/Users/me/.gemini/antigravity-cli/brain/x/.user_uploaded/../../../secret.png look",
		"look at " + agyUploaded,
	} {
		got := parseAntigravity(agyPrompt(t, 4, request))
		if len(got) != 1 || got[0].Text != request || len(got[0].Images) != 0 {
			t.Errorf("%q: %+v", request, got)
		}
	}
}

func TestAntigravityReadFileReadsAsARead(t *testing.T) {
	call := `{"step_index": 5, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-10-27T11:00:01Z", "tool_calls": [{"name": "read_file", "args": {"EndLine": 400, "Path": "/Users/me/code/shop/README.md", "StartLine": 1}}]}`
	got := parseAntigravity(call)
	if len(got) != 1 || got[0].Tool == nil || got[0].Tool.Name != "Read" || got[0].Tool.Summary != "/Users/me/code/shop/README.md" {
		t.Fatalf("%+v", got)
	}
}

func TestAntigravityAgentMessagesRead(t *testing.T) {
	call := `{"step_index": 7, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-10-27T11:00:01Z", "tool_calls": [{"name": "send_message", "args": {"Message": "The command exited with code 0 and printed:\nsubagent-check", "Recipient": "1bfba160-d7ef-4333-aea7-a4c0dfa1120e"}}]}`
	got := parseAntigravity(call)
	if len(got) != 1 || got[0].Tool.Name != "Message" || got[0].Tool.Summary != "The command exited with code 0 and printed:" {
		t.Fatalf("send_message: %+v", got)
	}

	note := `{"step_index": 2, "source": "SYSTEM", "type": "SYSTEM_MESSAGE", "status": "DONE", "created_at": "2026-10-05T17:06:29Z", "content": "The following is a <SYSTEM_MESSAGE> not actually sent by the user. It is provided by the system as important information to pay attention to.\n\n<SYSTEM_MESSAGE>\n[Message] timestamp=2026-10-05T17:06:29Z sender=1bfba160-d7ef-4333-aea7-a4c0dfa1120e priority=MESSAGE_PRIORITY_HIGH content=Run the shell command: echo subagent-check\nand report what it printed.\n</SYSTEM_MESSAGE>"}`
	got = parseAntigravity(note)
	if len(got) != 1 || got[0].Role != protocol.RoleSystem ||
		got[0].Text != "From another agent: Run the shell command: echo subagent-check and report what it printed." {
		t.Fatalf("system message: %+v", got)
	}
	notices := map[string]string{
		`sender=system priority=MESSAGE_PRIORITY_LOW content=[Notice] All your subagents and background tasks have been stopped due to server restart.`:                                                             "All your subagents and background tasks have been stopped due to server restart.",
		`sender=ef116096-84c3-4723-bbc3-aed0f3cca68b/task-80 priority=MESSAGE_PRIORITY_HIGH content=Task id \"ef116096-84c3-4723-bbc3-aed0f3cca68b/task-80\" finished with result: The command exited with code 0.`: `Task id "task-80" finished with result: The command exited with code 0.`,
	}
	for header, want := range notices {
		line := `{"step_index": 9, "source": "SYSTEM", "type": "SYSTEM_MESSAGE", "status": "DONE", "created_at": "2026-10-05T17:06:29Z", "content": "<SYSTEM_MESSAGE>\n[Message] timestamp=2026-10-05T17:06:29Z ` + header + `\n</SYSTEM_MESSAGE>"}`
		if got := parseAntigravity(line); len(got) != 1 || got[0].Text != want {
			t.Errorf("%s: %+v", header, got)
		}
	}
	other := `{"step_index": 3, "source": "SYSTEM", "type": "SYSTEM_MESSAGE", "status": "DONE", "created_at": "2026-10-05T17:06:29Z", "content": "<SYSTEM_MESSAGE>\nSomething else entirely.\n</SYSTEM_MESSAGE>"}`
	if got := parseAntigravity(other); len(got) != 0 {
		t.Fatalf("a system message of another kind showed: %+v", got)
	}
}

func TestAntigravityCompactionIsANotice(t *testing.T) {
	checkpoint := `{"step_index": 40, "source": "SYSTEM", "type": "CHECKPOINT", "status": "DONE", "created_at": "2026-10-20T09:00:00Z", "content": "# Resuming from a compaction\n\nYou are continuing work on the task described above..."}`
	got := parseAntigravity(checkpoint)
	if len(got) != 1 || got[0].Role != protocol.RoleSystem || got[0].Text != "Context compacted" {
		t.Fatalf("%+v", got)
	}
}

// agy writes a file named with @ as @[name], and where it is in the
// request's metadata (agy 1.4.8).
func TestAntigravityMentionsReadAsTyped(t *testing.T) {
	line := func(request, metadata string) string {
		raw, err := json.Marshal(map[string]any{
			"step_index": 4, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE",
			"created_at": "2026-10-27T11:12:47Z",
			"content": "<USER_REQUEST>\n" + request + "\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-10-27T11:12:47Z.\n\n" +
				"The user has mentioned some items in the form @[ITEM]. Here is extra information about the items that were mentioned by the user, in the order that they appear:\n\n" +
				metadata + "\n</ADDITIONAL_METADATA>",
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	cases := []struct{ request, metadata, want string }{
		{"reply with just OK about @[main.go]", "@[main.go] is a [File]:\n/Users/me/code/shop/main.go", "reply with just OK about @main.go"},
		{"@[report.pdf] summarize this", "@[report.pdf] is a [File]:\n/Users/me/.agentman/files/x/report.pdf", "@/Users/me/.agentman/files/x/report.pdf\n\nsummarize this"},
		{"@[a.md] @[b.md]", "@[a.md] is a [File]:\n/w/a.md\n\n@[b.md] is a [File]:\n/w/b.md", "@/w/a.md @/w/b.md"},
		{"@[src] look in here", "@[src] is a [Directory]:\n/w/src", "@src look in here"},
	}
	for _, c := range cases {
		got := parseAntigravity(line(c.request, c.metadata))
		if len(got) != 1 || got[0].Text != c.want {
			t.Errorf("%q: %+v", c.request, got)
		}
	}
}

// Cancelled at its permission question, a call gets no result at all (agy
// 1.4.8): it was not left running, the next prompt shows.
func TestAntigravityCancelledCallIsInterrupted(t *testing.T) {
	call := `{"step_index": 13, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-10-27T11:34:24Z", "tool_calls": [{"name": "run_command", "args": {"CommandLine": "sleep 40", "WaitMsBeforeAsync": 10000, "toolSummary": "Sleep 40 seconds"}}]}`
	first, next := agyPrompt(t, 12, "Run the shell command: sleep 40 then say done"), agyPrompt(t, 14, "never mind")

	got := parseAntigravity(first, call, next)
	if len(got) != 4 || got[1].Tool.Status != protocol.ToolRunning ||
		got[2].ID != got[1].ID || got[2].Tool.Status != protocol.ToolError || got[2].Text != "Interrupted" ||
		got[2].Tool.Summary != "sleep 40" || got[3].Text != "never mind" {
		t.Fatalf("forwards: %+v", got)
	}

	// Paging backwards, the next prompt is met first.
	p := NewAntigravityParser("antigravity:s")
	p.Parse(next, 0)
	if got := p.Parse(call, 0); len(got) != 1 || got[0].Tool.Status != protocol.ToolError || got[0].Text != "Interrupted" {
		t.Fatalf("backwards: %+v", got)
	}

	result := `{"step_index": 14, "source": "MODEL", "type": "GENERIC", "status": "DONE", "created_at": "2026-10-27T11:35:04Z", "content": "Created At: 2026-10-27T11:34:24Z\nCompleted At: 2026-10-27T11:35:04Z\n\nThe command exited with code 0.\nOutput:\n"}`
	got = parseAntigravity(first, call, result, agyPrompt(t, 15, "thanks"))
	for _, m := range got {
		if m.Text == "Interrupted" {
			t.Fatalf("a call with its result was interrupted: %+v", got)
		}
	}
}

// agy 1.4.8 calls an MCP server's tool through call_mcp_tool.
func TestAntigravityMCPCallIsNamedByServerAndTool(t *testing.T) {
	call := `{"step_index": 18, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-10-27T11:44:50Z", "tool_calls": [{"name": "call_mcp_tool", "args": {"Arguments": {"query": "release checklist"}, "ServerName": "agentman", "ToolName": "search_notes"}}]}`
	result := `{"step_index": 19, "source": "MODEL", "type": "GENERIC", "status": "DONE", "created_at": "2026-10-27T11:44:57Z", "content": "Created At: 2026-10-27T11:44:50Z\nCompleted At: 2026-10-27T11:44:57Z\n- Release checklist: how to ship"}`
	got := parseAntigravity(call, result)
	if len(got) != 2 || got[0].Tool.Name != "agentman.search_notes" || got[0].Tool.Summary != "release checklist" ||
		got[1].Tool.Status != protocol.ToolOK || got[1].Text != "- Release checklist: how to ship" {
		t.Fatalf("%+v %+v", got, got[len(got)-1].Tool)
	}
}
