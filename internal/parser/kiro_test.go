package parser

import (
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// These lines are what Kiro CLI 2.24.1 wrote for a real turn: a prompt, a
// shell call it paused on for approval, the call's result, and the reply. The
// redacted thinking arrays are shortened; nothing else is.
const (
	fixtureKiroPrompt = `{"version":"v1","kind":"Prompt","data":{"message_id":"53874b54-cd1a-4be4-b91b-097aee95c608","content":[{"kind":"text","data":"Run the shell command: ls -la   then tell me how many files there are."}],"meta":{"timestamp":1790528359}}}`

	fixtureKiroToolCall = `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"65d2be17-211e-4c75-83ee-81c49dd27bd5","content":[{"kind":"thinking","data":{"text":"","signature":null,"redactedContent":[46,75,84,82],"modelId":"auto"}},{"kind":"text","data":""},{"kind":"toolUse","data":{"toolUseId":"tooluse_oH7ogPsxlQvCUqyUEeHdON","name":"shell","input":{"command":"ls -la","__tool_use_purpose":"List directory contents"}}}]}}`

	fixtureKiroToolResult = `{"version":"v1","kind":"ToolResults","data":{"message_id":"8a45bdd7-fc5f-44f3-8172-cfcc85af23b3","content":[{"kind":"toolResult","data":{"toolUseId":"tooluse_oH7ogPsxlQvCUqyUEeHdON","content":[{"kind":"json","data":{"exit_status":"exit status: 0","stdout":"total 8\n-rw-r--r--@ 1 mac  wheel   6 Sep 27 17:59 notes.txt\n","stderr":""}}],"status":"success"}}],"results":{}}}`

	fixtureKiroReply = `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"6780d9c3-1c85-422e-8d50-385ed87c53c0","content":[{"kind":"thinking","data":{"text":"","redactedContent":[46,75]}},{"kind":"text","data":"There is **1 file**: ` + "`notes.txt`" + `"}]}}`

	fixtureKiroInterrupted = `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"56a837fd-7eea-4485-b30e-350b3bda8c51","content":[{"kind":"text","data":"Response was interrupted by the user"}]}}`
)

func parseKiro(lines ...string) []protocol.Message {
	p := NewKiroParser("kiro:s")
	var out []protocol.Message
	for i, line := range lines {
		out = append(out, p.Parse(line, int64(i*1000))...)
	}
	return out
}

func TestKiroTurnBecomesPromptToolAndReply(t *testing.T) {
	got := parseKiro(fixtureKiroPrompt, fixtureKiroToolCall, fixtureKiroToolResult, fixtureKiroReply)

	// Running call, then the same call settled, then the reply. The empty
	// text block and the redacted thinking produce nothing.
	if len(got) != 4 {
		t.Fatalf("got %d messages, want 4: %+v", len(got), got)
	}
	if got[0].Role != protocol.RoleUser || !strings.HasPrefix(got[0].Text, "Run the shell command") {
		t.Errorf("prompt = %+v", got[0])
	}
	running, settled := got[1], got[2]
	if running.ID != "tooluse_oH7ogPsxlQvCUqyUEeHdON" || running.Tool == nil ||
		running.Tool.Status != protocol.ToolRunning {
		t.Errorf("call = %+v", running)
	}
	if running.Tool.Name != "Shell" || running.Tool.Summary != "ls -la" {
		t.Errorf("call rendered as %q %q, want Shell ls -la", running.Tool.Name, running.Tool.Summary)
	}
	if settled.ID != running.ID || settled.Tool.Status != protocol.ToolOK {
		t.Errorf("result did not settle the call: %+v", settled)
	}
	if settled.Tool.Summary != "ls -la" {
		t.Errorf("settled row lost its command: %+v", settled.Tool)
	}
	if !strings.Contains(settled.Text, "notes.txt") {
		t.Errorf("result preview = %q", settled.Text)
	}
	if got[3].Role != protocol.RoleAssistant || !strings.Contains(got[3].Text, "1 file") {
		t.Errorf("reply = %+v", got[3])
	}
}

// Regression guard for the reason the parser keeps a clock at all: only
// prompts are timestamped, and the app sorts by ts. A reply with no time of
// its own would render above the prompt that produced it.
func TestKiroRepliesSortAfterTheirPrompt(t *testing.T) {
	got := parseKiro(fixtureKiroPrompt, fixtureKiroToolCall, fixtureKiroToolResult, fixtureKiroReply, fixtureKiroPrompt, fixtureKiroInterrupted)

	promptTs := int64(1790528359) * 1000
	if got[0].Ts != promptTs {
		t.Errorf("prompt ts = %d, want %d", got[0].Ts, promptTs)
	}
	// The settled row replaces the running one by id, so it must keep the
	// call's position rather than moving to the time it finished.
	if got[1].Ts != got[2].Ts {
		t.Errorf("settled tool moved from %d to %d", got[1].Ts, got[2].Ts)
	}
	last := int64(0)
	for i, message := range []protocol.Message{got[0], got[1], got[3], got[4], got[5]} {
		if message.Ts <= last {
			t.Fatalf("message %d (%s) ts %d does not sort after %d", i, message.Role, message.Ts, last)
		}
		last = message.Ts
	}
}

func TestKiroInterruptedTurnIsShown(t *testing.T) {
	got := parseKiro(fixtureKiroPrompt, fixtureKiroInterrupted)
	if len(got) != 2 || got[1].Text != "Response was interrupted by the user" {
		t.Fatalf("got %+v", got)
	}
}

func TestKiroFailingCommandIsAnError(t *testing.T) {
	failed := strings.Replace(fixtureKiroToolResult, "exit status: 0", "exit status: 2", 1)
	got := parseKiro(fixtureKiroPrompt, fixtureKiroToolCall, failed)
	if got[2].Tool.Status != protocol.ToolError {
		t.Errorf("a non-zero exit was reported as %s", got[2].Tool.Status)
	}
}

func TestKiroFileWriteIsSummarizedByPathNotEditKind(t *testing.T) {
	write := `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m","content":[{"kind":"toolUse","data":{"toolUseId":"t1","name":"fs_write","input":{"command":"create","path":"/work/main.go","file_text":"package main"}}}]}}`
	got := parseKiro(fixtureKiroPrompt, write)
	if got[1].Tool.Name != "Write" || got[1].Tool.Summary != "/work/main.go" {
		t.Errorf("fs_write rendered as %q %q", got[1].Tool.Name, got[1].Tool.Summary)
	}
}

// The lines below keep the shapes Kiro CLI 2.27.1 wrote in a research session
// (~/.kiro/sessions/cli, 2026-10-05). Paths are moved under /work, the
// redacted thinking blocks and the results map are dropped, and image bytes
// are shortened; nothing else is changed.

func kiroCallLine(id, name, input string) string {
	return `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"a-` + id + `","content":[{"kind":"text","data":""},` +
		`{"kind":"toolUse","data":{"toolUseId":"` + id + `","name":"` + name + `","input":` + input + `}}]}}`
}

func kiroResultLine(id, content, status string) string {
	return `{"version":"v1","kind":"ToolResults","data":{"message_id":"r-` + id + `","content":[{"kind":"toolResult",` +
		`"data":{"toolUseId":"` + id + `","content":` + content + `,"status":"` + status + `"}}],"results":{}}}`
}

// rows returns the last message for each id, in first-seen order: the feed as
// the app shows it once running rows have settled.
func rows(messages []protocol.Message) []protocol.Message {
	at := map[string]int{}
	var out []protocol.Message
	for _, message := range messages {
		if index, seen := at[message.ID]; seen {
			out[index] = message
			continue
		}
		at[message.ID] = len(out)
		out = append(out, message)
	}
	return out
}

// Kiro's read tool takes images in a list of their own. Before, the row was
// summarised by the call's stated purpose ("Read red.png to determine its
// color"), so the app could not open the picture the agent looked at and the
// daemon never recorded the file as one it read.
func TestKiroImageReadNamesThePictureItOpened(t *testing.T) {
	got := rows(parseKiro(fixtureKiroPrompt,
		kiroCallLine("t1", "read", `{"__tool_use_purpose":"Read red.png to determine its color","operations":[{"mode":"Image","image_paths":["/work/red.png","/work/blue.png"]}]}`),
		kiroResultLine("t1", `[{"kind":"image","data":{"format":"png","source":{"kind":"bytes","data":[137,80,78,71]}}},`+
			`{"kind":"image","data":{"format":"png","source":{"kind":"bytes","data":[137,80,78,71]}}}]`, "success")))
	if len(got) != 3 {
		t.Fatalf("got %d rows, want the prompt and one per image: %+v", len(got), got)
	}
	for i, path := range []string{"/work/red.png", "/work/blue.png"} {
		row := got[i+1]
		if row.Tool.Name != "Read" || row.Tool.Summary != path || row.Text != "[image]" || row.Tool.Status != protocol.ToolOK {
			t.Errorf("image %d = %q %q %q %s", i, row.Tool.Name, row.Tool.Summary, row.Text, row.Tool.Status)
		}
	}
	if got[1].ID != "t1" || got[2].ID == got[1].ID {
		t.Errorf("ids %q %q: the first row must keep the call's id and the second need its own", got[1].ID, got[2].ID)
	}
}

// One read call often batches several files. A row per file is how Claude's
// feed reads, and gives each file its own preview.
func TestKiroBatchedReadIsARowPerFile(t *testing.T) {
	got := rows(parseKiro(fixtureKiroPrompt,
		kiroCallLine("t1", "read", `{"__tool_use_purpose":"Reading core source files","operations":[{"mode":"Line","path":"/work/src/main.rs"},{"mode":"Line","path":"/work/Cargo.toml","limit":50},{"mode":"Directory","path":"/work/src","depth":2}]}`),
		kiroResultLine("t1", `[{"kind":"text","data":"fn main() {}"},{"kind":"text","data":"[package]"},{"kind":"text","data":"User id: 501\ndrwxr-xr-x 8 501 20 256 Mar 20 05:04 /work/src/marc"}]`, "success")))
	want := []struct{ name, summary, text string }{
		{"Read", "/work/src/main.rs", "fn main() {}"},
		{"Read", "/work/Cargo.toml", "[package]"},
		{"List", "/work/src", "User id: 501\ndrwxr-xr-x 8 501 20 256 Mar 20 05:04 /work/src/marc"},
	}
	if len(got) != len(want)+1 {
		t.Fatalf("got %d rows: %+v", len(got), got)
	}
	for i, w := range want {
		row := got[i+1]
		if row.Tool.Name != w.name || row.Tool.Summary != w.summary || row.Text != w.text {
			t.Errorf("row %d = %q %q %q, want %+v", i, row.Tool.Name, row.Tool.Summary, row.Text, w)
		}
	}
}

// A grep or glob was summarised by the directory it ran in — the "path" key
// won over "pattern" — and its structured result was shown as raw JSON.
func TestKiroSearchesShowTheirPatternAndWhatTheyFound(t *testing.T) {
	got := rows(parseKiro(fixtureKiroPrompt,
		kiroCallLine("g", "grep", `{"__tool_use_purpose":"Search for the word beta","pattern":"beta","path":"/work"}`),
		kiroCallLine("f", "glob", `{"__tool_use_purpose":"Find all PNG files","pattern":"**/*.png","path":"/work"}`),
		kiroResultLine("g", `[{"kind":"json","data":{"numMatches":1,"numFiles":1,"truncated":false,"results":[{"file":"/work/notes.txt","count":1,"matches":["2:beta"]}]}}]`, "success"),
		kiroResultLine("f", `[{"kind":"json","data":{"filePaths":["/work/blue.png","/work/img/red.png"],"totalFiles":2,"truncated":false}}]`, "success")))
	grep, glob := got[1], got[2]
	if grep.Tool.Name != "Grep" || grep.Tool.Summary != "beta" || grep.Text != "notes.txt:2:beta" {
		t.Errorf("grep = %q %q %q", grep.Tool.Name, grep.Tool.Summary, grep.Text)
	}
	if glob.Tool.Name != "Glob" || glob.Tool.Summary != "**/*.png" || glob.Text != "blue.png\nimg/red.png" {
		t.Errorf("glob = %q %q %q", glob.Tool.Name, glob.Tool.Summary, glob.Text)
	}
}

// An edit is shown as a diff from the moment it is proposed, which is when the
// phone is asked to approve it, and stays once it succeeds: "Successfully
// replaced 1 occurrence(s)" says nothing about what changed. A denial shows
// why instead.
func TestKiroFileEditsAreDiffs(t *testing.T) {
	create := kiroCallLine("c", "write", `{"__tool_use_purpose":"Create greeting.txt","command":"create","path":"/work/greeting.txt","content":"hi there\n"}`)
	replace := kiroCallLine("r", "write", `{"__tool_use_purpose":"Replace text","command":"strReplace","path":"/work/greeting.txt","oldStr":"hi there","newStr":"hello there\nand more"}`)
	insert := kiroCallLine("i", "write", `{"command":"insert","path":"/work/greeting.txt","insertLine":4,"content":"tail"}`)
	all := parseKiro(fixtureKiroPrompt,
		create, kiroResultLine("c", `[{"kind":"text","data":"User denied tool execution. Feedback: Please name it hello.txt instead"}]`, "error"),
		replace, kiroResultLine("r", `[{"kind":"text","data":"Successfully replaced 1 occurrence(s) in /work/greeting.txt."}]`, "success"),
		insert)

	running := all[1]
	if running.Tool.Status != protocol.ToolRunning || running.Text != "@@ -0,0 +1,1 @@\n+hi there" {
		t.Errorf("proposed file = %q (%s), want its content as additions", running.Text, running.Tool.Status)
	}
	got := rows(all)
	if denied := got[1]; denied.Tool.Status != protocol.ToolError || !strings.Contains(denied.Text, "Please name it hello.txt") {
		t.Errorf("denied write = %q (%s)", denied.Text, denied.Tool.Status)
	}
	edit := got[2]
	if edit.Tool.Name != "Edit" || edit.Tool.Summary != "/work/greeting.txt" ||
		edit.Text != "@@ -1,1 +1,2 @@\n-hi there\n+hello there\n+and more" || edit.Tool.Status != protocol.ToolOK {
		t.Errorf("edit = %q %q %q %s", edit.Tool.Name, edit.Tool.Summary, edit.Text, edit.Tool.Status)
	}
	if inserted := got[3]; inserted.Text != "@@ -4,0 +5,1 @@\n+tail" {
		t.Errorf("insert = %q", inserted.Text)
	}
}

// Kiro's task list is the closest thing it has to a plan. Its result is the
// whole list, which reads as a checklist rather than as JSON, and a call that
// ticks off task "1" names the task.
func TestKiroTodoListReadsAsAChecklist(t *testing.T) {
	got := rows(parseKiro(fixtureKiroPrompt,
		kiroCallLine("c", "todo_list", `{"__tool_use_purpose":"Create a checklist","command":"create","task_list_description":"Write a haiku","tasks":[{"task_description":"Choose a theme"},{"task_description":"Draft it"}]}`),
		kiroResultLine("c", `[{"kind":"json","data":{"tasks":[{"id":"1","task_description":"Choose a theme","completed":false},{"id":"2","task_description":"Draft it","completed":false}],"description":"Write a haiku","context":[],"modified_files":[]}}]`, "success"),
		kiroCallLine("d", "todo_list", `{"__tool_use_purpose":"Mark task 1 as complete","command":"complete","completed_task_ids":["1"],"context_update":"Theme chosen"}`),
		kiroResultLine("d", `[{"kind":"json","data":{"tasks":[{"id":"1","task_description":"Choose a theme","completed":true},{"id":"2","task_description":"Draft it","completed":false}],"description":"Write a haiku","context":["Theme chosen"],"modified_files":[]}}]`, "success"),
		kiroCallLine("e", "todo_list", `{"command":"complete","completed_task_ids":["2"]}`),
		kiroResultLine("e", `[{"kind":"json","data":{"tasks":[],"description":"","context":[],"modified_files":[]}}]`, "success")))
	created, done, last := got[1], got[2], got[3]
	if created.Tool.Name != "Todo list" || created.Tool.Summary != "Write a haiku" ||
		created.Text != "Write a haiku\n[ ] Choose a theme\n[ ] Draft it" {
		t.Errorf("created = %q %q %q", created.Tool.Name, created.Tool.Summary, created.Text)
	}
	if done.Tool.Summary != "Done: Choose a theme" || !strings.Contains(done.Text, "[x] Choose a theme") {
		t.Errorf("ticked = %q %q", done.Tool.Summary, done.Text)
	}
	if last.Tool.Summary != "Done: Draft it" || last.Text != "No tasks left" {
		t.Errorf("finished = %q %q", last.Tool.Summary, last.Text)
	}
}

// A message typed while Kiro works reaches the model inside the next tool
// results. It used to be dropped, so the feed showed the agent changing course
// for no visible reason; and the note Kiro asks the agent to append about it
// arrived as a raw "[STEERING steer-…: …]" tag.
func TestKiroSteeringIsShownAsTheUsersMessage(t *testing.T) {
	steering := `{"version":"v1","kind":"ToolResults","data":{"message_id":"r1","content":[{"kind":"toolResult","data":{"toolUseId":"t1","content":[{"kind":"json","data":{"exit_status":"exit status: 0","stdout":"ok\n","stderr":""}}],"status":"success"}},` +
		`{"kind":"text","data":"[LIVE STEERING - New message from user]\n\nThe user sent a new message while you are working. As the currently active agent, adjust your approach if necessary based on this guidance.\n\n<user_message id=\"steer-b8ce6471f8034f67acd1b9147f2b4502\">\nSteer: make the haiku about phones instead.\n</user_message>\n\nIMPORTANT: After completing your work, include a brief note about how you handled this steering message. Use this exact format:\n\n[STEERING steer-b8ce6471f8034f67acd1b9147f2b4502: <describe what you did or why it wasn't applicable>]"}]}}`
	reply := `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"a2","content":[{"kind":"text","data":"Here's the haiku.\n\n[STEERING steer-b8ce6471f8034f67acd1b9147f2b4502: Switched the subject from tmux to phones.]"}]}}`
	got := parseKiro(fixtureKiroPrompt, fixtureKiroToolCall, steering, reply)

	var steer, answer *protocol.Message
	for i := range got {
		switch {
		case got[i].ID == "steer-b8ce6471f8034f67acd1b9147f2b4502":
			steer = &got[i]
		case got[i].Role == protocol.RoleAssistant:
			answer = &got[i]
		}
	}
	if steer == nil || steer.Role != protocol.RoleUser || steer.Text != "Steer: make the haiku about phones instead." {
		t.Fatalf("steering message = %+v", steer)
	}
	if answer == nil || answer.Text != "Here's the haiku.\n\nSwitched the subject from tmux to phones." {
		t.Fatalf("reply = %+v", answer)
	}
	if steer.Ts <= got[1].Ts || answer.Ts <= steer.Ts {
		t.Errorf("order: call %d, steer %d, reply %d", got[1].Ts, steer.Ts, answer.Ts)
	}
}

// Kiro attaches a picture itself when a prompt names it by path — how a
// photo sent from the phone arrives — and records it as an image block.
func TestKiroPromptImageIsMarked(t *testing.T) {
	prompt := `{"version":"v1","kind":"Prompt","data":{"message_id":"p1","content":[{"kind":"image","data":{"format":"png","source":{"kind":"bytes","data":[137,80,78,71]}}},{"kind":"text","data":"what color is this one? /work/blue.png"}],"meta":{"timestamp":1791193151}}}`
	got := parseKiro(prompt)
	if len(got) != 1 || got[0].Text != "[image]\n\nwhat color is this one? /work/blue.png" {
		t.Fatalf("got %+v", got)
	}
	bare := strings.Replace(prompt, `,{"kind":"text","data":"what color is this one? /work/blue.png"}`, "", 1)
	if got := parseKiro(bare); len(got) != 1 || got[0].Text != "[image]" {
		t.Errorf("an image with no words was dropped: %+v", got)
	}
}

// Records that change the conversation rather than add to it, and the replies
// Kiro writes in the agent's name when the user stopped it, are notices.
func TestKiroConversationEventsAreNotices(t *testing.T) {
	compaction := `{"version":"v1","kind":"Compaction","data":{"summary":"## OBJECTIVE\nNo active user task.","strategy":{"message_pairs_to_exclude":2}}}`
	clear := `{"version":"v1","kind":"Clear","data":null}`
	cancelled := `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"a3","content":[{"kind":"text","data":"Tool uses were interrupted, waiting for the next user prompt"}]}}`
	got := parseKiro(fixtureKiroPrompt, fixtureKiroInterrupted, compaction, clear, cancelled)
	want := []string{"Response was interrupted by the user", "Context compacted", "Conversation cleared",
		"Tool uses were interrupted, waiting for the next user prompt"}
	if len(got) != len(want)+1 {
		t.Fatalf("got %+v", got)
	}
	for i, text := range want {
		if got[i+1].Role != protocol.RoleSystem || got[i+1].Text != text {
			t.Errorf("notice %d = %s %q, want system %q", i, got[i+1].Role, got[i+1].Text, text)
		}
	}
	if got[2].ID == got[3].ID {
		t.Error("two notices without ids of their own share one")
	}
}

// A call left without a result — Kiro closed while it waited for approval —
// used to spin as "running" for ever. The next prompt proves it is over.
func TestKiroCallLeftWithoutAResultFailsWhenTheNextTurnStarts(t *testing.T) {
	next := strings.Replace(fixtureKiroPrompt, `"message_id":"53874b54`, `"message_id":"next`, 1)
	got := rows(parseKiro(fixtureKiroPrompt, fixtureKiroToolCall, next))
	if len(got) != 3 || got[1].Tool == nil || got[1].Tool.Status != protocol.ToolError || got[1].Tool.Summary != "ls -la" {
		t.Fatalf("got %+v", got)
	}
}

// Reply texts are named after their place in the turn, so the streaming
// preview — written before Kiro records the reply — can carry the same id and
// be replaced in place.
func TestKiroReplyTextIsNamedAfterItsTurn(t *testing.T) {
	p := NewKiroParser("kiro:s")
	var got []protocol.Message
	for i, line := range []string{fixtureKiroPrompt, fixtureKiroToolCall, fixtureKiroToolResult, fixtureKiroReply} {
		got = append(got, p.Parse(line, int64(i*1000))...)
	}
	reply := got[len(got)-1]
	if reply.ID != KiroTextID("53874b54-cd1a-4be4-b91b-097aee95c608", 1) {
		t.Errorf("reply id = %q", reply.ID)
	}
	promptID, texts, nextTs := p.TurnState()
	if promptID != "53874b54-cd1a-4be4-b91b-097aee95c608" || texts != 1 || nextTs <= reply.Ts {
		t.Errorf("turn state = %q %d %d (reply ts %d)", promptID, texts, nextTs, reply.Ts)
	}
	// The same lines read again name the same rows: paging and following
	// must agree.
	again := parseKiro(fixtureKiroPrompt, fixtureKiroToolCall, fixtureKiroToolResult, fixtureKiroReply)
	if again[len(again)-1].ID != reply.ID {
		t.Errorf("a second read named the reply %q", again[len(again)-1].ID)
	}
}

// A subagent call is summarised by its task, and a subagent's own report —
// handed over in its summary call, whose result is empty — is what its row
// shows.
func TestKiroSubagentCallsShowTheirTask(t *testing.T) {
	got := rows(parseKiro(fixtureKiroPrompt,
		kiroCallLine("s", "subagent", `{"__tool_use_purpose":"Delegate reading notes.txt","task":"Read /work/notes.txt and report its first line.","stages":[{"name":"read_notes","role":"kiro_default","prompt_template":"Read /work/notes.txt and report its first line."}]}`),
		kiroResultLine("s", `[{"kind":"text","data":"Pipeline completed: 1 stages finished.\n\n## read_notes\n\nThe first line is: alpha"}]`, "success"),
		kiroCallLine("r", "summary", `{"__tool_use_purpose":"Reporting the result","taskDescription":"Read /work/notes.txt","taskResult":"The first line is alpha"}`),
		kiroResultLine("r", `[{"kind":"text","data":""}]`, "success")))
	sub, report := got[1], got[2]
	if sub.Tool.Name != "Subagent" || sub.Tool.Summary != "Read /work/notes.txt and report its first line." ||
		!strings.Contains(sub.Text, "alpha") {
		t.Errorf("subagent = %q %q %q", sub.Tool.Name, sub.Tool.Summary, sub.Text)
	}
	if report.Tool.Name != "Summary" || report.Text != "The first line is alpha" {
		t.Errorf("report = %q %q", report.Tool.Name, report.Text)
	}
}

// The model owes the turn a message after a prompt and after the results of
// every call it made — exactly when Kiro's screen shows a reply its
// transcript does not have yet.
func TestKiroAwaitingReply(t *testing.T) {
	p := NewKiroParser("kiro:s")
	steps := []struct {
		line string
		want bool
	}{
		{fixtureKiroPrompt, true},
		{fixtureKiroToolCall, false},
		{fixtureKiroToolResult, true},
		{fixtureKiroReply, false},
	}
	for i, step := range steps {
		p.Parse(step.line, int64(i))
		if got := p.AwaitingReply(); got != step.want {
			t.Errorf("after line %d: awaiting = %v, want %v", i, got, step.want)
		}
	}
	// Two calls, one result: the other call is still running.
	p = NewKiroParser("kiro:s")
	p.Parse(fixtureKiroPrompt, 0)
	p.Parse(kiroCallLine("a", "shell", `{"command":"ls"}`), 1)
	p.Parse(strings.Replace(kiroCallLine("b", "shell", `{"command":"pwd"}`), `"message_id":"a-b"`, `"message_id":"a-b2"`, 1), 2)
	p.Parse(kiroResultLine("a", `[{"kind":"text","data":"x"}]`, "success"), 3)
	if p.AwaitingReply() {
		t.Error("awaiting a reply while a call is still running")
	}
}
