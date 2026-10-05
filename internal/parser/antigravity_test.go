package parser

import (
	"os"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Steps Antigravity CLI 1.2.12 wrote for one real turn. Thinking and the tail
// of long strings are cut; the rest is as written, carriage returns included.
const (
	agStepPrompt = `{"step_index": 0, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE", "created_at": "2026-09-27T17:02:00Z", "content": "<USER_REQUEST>\nRun the shell command: ls -la   then tell me how many files there are.\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-09-27T18:02:00+01:00.\n</ADDITIONAL_METADATA>"}`
	agStepCall   = `{"step_index": 1, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-27T17:02:00Z", "tool_calls": [{"name": "run_command", "args": {"CommandLine": "ls -la", "Cwd": "/work", "WaitMsBeforeAsync": 5000, "toolAction": "Listing files", "toolSummary": "Run ls -la"}}]}`
	agStepResult = `{"step_index": 2, "source": "MODEL", "type": "GENERIC", "status": "DONE", "created_at": "2026-09-27T17:02:03Z", "content": "Created At: 2026-09-27T18:02:03+01:00\nCompleted At: 2026-09-27T18:02:41+01:00\n\nThe command exited with code 0.\nOutput:\ntotal 8\r\n-rw-r--r--@ 1 mac  wheel    6 Sep 27 18:01 notes.txt\r\n\n"}`
	agStepReply  = `{"step_index": 3, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-27T17:02:41Z", "content": "There is **1** regular file."}`
)

func parseAntigravity(lines ...string) []protocol.Message {
	p := NewAntigravityParser("antigravity:s")
	var out []protocol.Message
	for i, line := range lines {
		out = append(out, p.Parse(line, int64(i*1000))...)
	}
	return out
}

func TestAntigravityTurnBecomesPromptToolAndReply(t *testing.T) {
	got := parseAntigravity(agStepPrompt, agStepCall, agStepResult, agStepReply)
	if len(got) != 4 {
		t.Fatalf("got %d messages, want 4: %+v", len(got), got)
	}
	if got[0].Role != protocol.RoleUser ||
		got[0].Text != "Run the shell command: ls -la   then tell me how many files there are." {
		t.Errorf("prompt kept Antigravity's own wrapper: %q", got[0].Text)
	}
	running, settled := got[1], got[2]
	if running.Tool == nil || running.Tool.Name != "Shell" || running.Tool.Summary != "ls -la" ||
		running.Tool.Status != protocol.ToolRunning {
		t.Errorf("call = %+v", running)
	}
	if settled.ID != running.ID || settled.Tool.Status != protocol.ToolOK || settled.Ts != running.Ts {
		t.Errorf("result did not settle the call in place: %+v vs %+v", settled, running)
	}
	if strings.Contains(settled.Text, "Created At") || !strings.Contains(settled.Text, "notes.txt") {
		t.Errorf("result preview = %q", settled.Text)
	}
	if got[3].Role != protocol.RoleAssistant || got[3].Text != "There is **1** regular file." {
		t.Errorf("reply = %+v", got[3])
	}
}

// Paging reads backwards and meets each result before its call. The call must
// still come out as one settled row, not a running one and an orphan.
func TestAntigravityReadsBackwardsToTheSameRows(t *testing.T) {
	got := parseAntigravity(agStepReply, agStepResult, agStepCall, agStepPrompt)
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3: %+v", len(got), got)
	}
	call := got[1]
	if call.Tool == nil || call.Tool.Status != protocol.ToolOK || !strings.Contains(call.Text, "notes.txt") {
		t.Errorf("the call did not pick up its result: %+v", call)
	}
	forward := parseAntigravity(agStepPrompt, agStepCall, agStepResult, agStepReply)
	if call.ID != forward[2].ID {
		t.Errorf("backwards id %q, forwards %q: the two reads would not merge", call.ID, forward[2].ID)
	}
}

// The prompt and the tool call share a second. The app breaks ties by id, so
// ids must sort in step order: an unpadded "s10" would land before "s9".
func TestAntigravitySameSecondStepsKeepTheirOrder(t *testing.T) {
	got := parseAntigravity(agStepPrompt, agStepCall)
	if got[0].Ts != got[1].Ts {
		t.Fatalf("fixture no longer shares a second: %d vs %d", got[0].Ts, got[1].Ts)
	}
	if !(got[0].ID < got[1].ID) {
		t.Errorf("ids %q and %q sort out of step order", got[0].ID, got[1].ID)
	}
	if !(antigravityID(9) < antigravityID(10)) {
		t.Error("step 10 sorts before step 9")
	}
}

func TestAntigravityFailingCommandIsAnError(t *testing.T) {
	failed := strings.Replace(agStepResult, "exited with code 0", "exited with code 1", 1)
	got := parseAntigravity(agStepPrompt, agStepCall, failed)
	if got[2].Tool.Status != protocol.ToolError {
		t.Errorf("a non-zero exit was reported as %s", got[2].Tool.Status)
	}
}

// A turn interrupted before it said anything leaves a response holding only
// thinking. There is nothing to show for it.
func TestAntigravityThinkingOnlyResponseIsSilent(t *testing.T) {
	interrupted := `{"step_index": 5, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-27T17:03:00Z", "thinking": "Considering the essay"}`
	if got := parseAntigravity(interrupted); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

// A reply is being written exactly while the newest record is a prompt.
//
// The session's state cannot answer this: it comes from discovery, which
// sweeps on its own timer and still reads busy for up to a sweep after a
// reply lands. Streaming on that stale answer sent the finished reply a
// second time, scraped off the pane and wrapped to the terminal's width.
func TestAntigravityAwaitingResponseFollowsTheTranscript(t *testing.T) {
	p := NewAntigravityParser("antigravity:c1")
	if p.AwaitingResponse() {
		t.Error("awaiting a reply before any prompt was seen")
	}

	p.Parse(`{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"write it"}`, 0)
	if !p.AwaitingResponse() {
		t.Error("a prompt with no reply yet should be awaiting one")
	}
	if got := p.NextStepIndex(); got != 1 {
		t.Errorf("next step = %d, want 1", got)
	}

	p.Parse(`{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE","content":"done"}`, 1)
	if p.AwaitingResponse() {
		t.Error("still awaiting a reply after its record landed; the pane copy would be sent again")
	}

	// And the next turn starts the cycle over.
	p.Parse(`{"step_index":2,"type":"USER_INPUT","status":"DONE","content":"again"}`, 2)
	if !p.AwaitingResponse() {
		t.Error("a second prompt should be awaiting a reply")
	}
	if got := p.NextStepIndex(); got != 3 {
		t.Errorf("next step = %d, want 3", got)
	}
}

// Steps agy 1.2.17 wrote for a session of harmless research prompts — a file
// written and edited, a command, an image read, a declined edit and URL, a
// question, a plan artifact, a subagent and a background command — with
// thinking dropped and paths shortened.
func antigravityFixture(t *testing.T) map[string]protocol.Message {
	t.Helper()
	raw, err := os.ReadFile("testdata/antigravity_1.2.17.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	p := NewAntigravityParser("antigravity:s")
	byID := map[string]protocol.Message{}
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		for _, m := range p.Parse(line, int64(i)) {
			byID[m.ID] = m
		}
	}
	return byID
}

func TestAntigravityToolRowsReadWithoutAgyNotesToTheModel(t *testing.T) {
	rows := antigravityFixture(t)
	for _, tc := range []struct {
		id, name, summary, text string
		status                  protocol.ToolStatus
	}{
		// A new file reads as the lines that went into it.
		{"s000009", "Write", "/work/ws1/notes.txt", "@@ -0,0 +1,1 @@\n+hello", protocol.ToolOK},
		// A text read loses its header and the note about line numbers.
		{"s000011", "Read", "/work/ws1/notes.txt", "1: hello", protocol.ToolOK},
		// A declined edit says so once.
		{"s000013", "Edit", "/work/ws1/notes.txt",
			"user denied permission for write_file(/work/ws1/notes.txt)", protocol.ToolError},
		// A command shows what it printed, not agy's framing of it.
		{"s000016", "Shell", "sleep 3; echo agentman-research", "agentman-research", protocol.ToolOK},
		// An image read is an image, as on Claude's rows.
		{"s000020", "Read", "/work/ws1/shot.png", "[image]", protocol.ToolOK},
		{"s000036", "Question", "Which colour do you prefer?", "A1: purple", protocol.ToolOK},
		// A plan is written with write_to_file and marked as an artifact.
		{"s000046", "Artifact",
			"/Users/me/.gemini/antigravity-cli/brain/ef116096-84c3-4723-bbc3-aed0f3cca68b/implementation_plan.md",
			"Implementation plan and task list to add README.md containing 'research workspace'.", protocol.ToolOK},
		// A call the model got wrong keeps only the reason.
		{"s000044", "Read", "/work/ws1/README.md",
			"failed to read file: stat /work/ws1/README.md: no such file or directory", protocol.ToolError},
		{"s000062", "Web fetch", "https://example.com", "user denied permission for read_url(example.com)", protocol.ToolError},
		// The role and its instructions; the result is agy's receipt, not output.
		{"s000065", "Agent", "Workspace Inspector: Inspect the directory /work/ws1.…", "Started 1 subagent.", protocol.ToolOK},
		// Nothing later settles a background command, so it must not spin.
		{"s000080", "Shell", "sleep 12; echo bg-done", "Running in the background as task-80.", protocol.ToolOK},
		// An edit's own unified diff, without the markers around it.
		{"s000092", "Edit", "/work/ws1/notes.txt", "@@ -1,2 +1,3 @@\n-hello\n+hello world\n+bye", protocol.ToolOK},
	} {
		row, ok := rows[tc.id]
		if !ok || row.Tool == nil {
			t.Errorf("%s: no tool row", tc.id)
			continue
		}
		summary := row.Tool.Summary
		if prefix, clipped := strings.CutSuffix(tc.summary, "…"); clipped && strings.HasPrefix(summary, prefix) {
			summary = tc.summary
		}
		if row.Tool.Name != tc.name || summary != tc.summary || row.Tool.Status != tc.status {
			t.Errorf("%s: %s %q %s, want %s %q %s", tc.id, row.Tool.Name, row.Tool.Summary, row.Tool.Status,
				tc.name, tc.summary, tc.status)
		}
		if tc.text != "" && strings.TrimSpace(row.Text) != tc.text {
			t.Errorf("%s: text\n%q\nwant\n%q", tc.id, row.Text, tc.text)
		}
		for _, note := range []string{"If relevant, proactively", "Do not attempt", "Encountered error",
			"[diff_block_start]", "Created At", "The following code has been modified", "Total Lines"} {
			if strings.Contains(row.Text, note) {
				t.Errorf("%s kept agy's note %q: %q", tc.id, note, row.Text)
			}
		}
	}
	// A result whose call is outside what was read is not shown on its own.
	if _, ok := rows["s000090"]; ok {
		t.Error("an orphaned result became a row")
	}
}

func TestAntigravitySearchShowsTheQuery(t *testing.T) {
	got := parseAntigravity(
		`{"step_index":40,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-27T18:48:20Z","tool_calls":[{"name":"search_web","args":{"query":"UN General Assembly September 2026","toolAction":"Searching","toolSummary":"Search UNGA"}}]}`,
		`{"step_index":41,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-09-27T18:48:26Z","content":"Created At: 2026-09-27T19:48:20+01:00\nCompleted At: 2026-09-27T19:48:26+01:00\nThe search for \"UN General Assembly September 2026\" returned the following summary:\nThe **81st session** opened on 8 September."}`)
	if len(got) != 2 || got[1].Tool.Name != "Web search" || got[1].Tool.Summary != "UN General Assembly September 2026" ||
		got[1].Text != "The **81st session** opened on 8 September." {
		t.Errorf("got %+v", got)
	}
}

// A path is kept whole however long it is: clipped, the phone cannot open it
// and the daemon does not record it as a file the agent touched.
func TestAntigravityLongPathsAreNotClipped(t *testing.T) {
	long := "/Users/me/" + strings.Repeat("deeply/nested/", 20) + "shot.png"
	got := parseAntigravity(`{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE","tool_calls":[{"name":"view_file","args":{"AbsolutePath":"` + long + `"}}]}`)
	if len(got) != 1 || got[0].Tool.Summary != long {
		t.Errorf("summary = %q", got[0].Tool.Summary)
	}
}

// Read backwards, as Page does, every row comes out the same: the result is
// met first and its call lays the written file over it.
func TestAntigravityFixtureReadsTheSameBackwards(t *testing.T) {
	raw, err := os.ReadFile("testdata/antigravity_1.2.17.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	reversed := make([]string, len(lines))
	for i, line := range lines {
		reversed[len(lines)-1-i] = line
	}
	forward := antigravityFixture(t)
	for _, m := range parseAntigravity(reversed...) {
		want := forward[m.ID]
		if m.Text != want.Text || (m.Tool == nil) != (want.Tool == nil) ||
			(m.Tool != nil && *m.Tool != *want.Tool) {
			t.Errorf("%s backwards %+v %v, forwards %+v %v", m.ID, m, m.Tool, want, want.Tool)
		}
	}
}

// An empty file has no lines to show as a diff, so the row keeps agy's own
// receipt — without the instruction to the model that follows it.
func TestAntigravityEmptyWriteKeepsTheReceiptOnly(t *testing.T) {
	got := parseAntigravity(
		`{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE","tool_calls":[{"name":"write_to_file","args":{"TargetFile":"/w/empty.txt","CodeContent":"","EmptyFile":true}}]}`,
		`{"step_index":2,"type":"GENERIC","status":"DONE","content":"Created At: 2026-10-05T10:30:45+01:00\nCompleted At: 2026-10-05T10:30:56+01:00\nCreated file file:///w/empty.txt with requested content.\nIf relevant, proactively run terminal commands to execute this code for the USER. Don't ask for permission."}`)
	if len(got) != 2 || got[1].Text != "Created file file:///w/empty.txt with requested content." {
		t.Errorf("got %q", got[len(got)-1].Text)
	}
}

// The model writes a reply after a tool's result just as after a prompt — the
// answer to "run ls, then tell me…" comes after the ls. A declined call is the
// exception: agy ends the turn there.
func TestAntigravityAwaitsTheReplyToAToolResult(t *testing.T) {
	p := NewAntigravityParser("antigravity:c1")
	p.Parse(agStepPrompt, 0)
	p.Parse(agStepCall, 1)
	if p.AwaitingResponse() {
		t.Error("awaiting a reply while the tool runs")
	}
	p.Parse(agStepResult, 2)
	if !p.AwaitingResponse() || p.NextStepIndex() != 3 {
		t.Errorf("after the result: awaiting %v next %d", p.AwaitingResponse(), p.NextStepIndex())
	}
	p.Parse(`{"step_index":5,"type":"GENERIC","status":"ERROR","error":"permission check failed for read_url \"example.com\": user denied permission for read_url(example.com)"}`, 3)
	if p.AwaitingResponse() {
		t.Error("awaiting a reply after the user declined the call")
	}
}
