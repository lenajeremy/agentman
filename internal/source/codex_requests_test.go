package source

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// The approval Codex 0.166 sent for `echo hi > note.txt`, as captured.
const realCommandApproval = `{"kind":"command","threadId":"t1","turnId":"u1","itemId":"exec-1","startedAtMs":1,
"environmentId":"local","command":"/bin/zsh -lc 'echo hi > note.txt'","cwd":"/work/api",
"commandActions":[{"type":"unknown","command":"echo hi > note.txt"}],
"proposedExecpolicyAmendment":["/bin/zsh","-lc","echo hi > note.txt"],
"availableDecisions":["accept",{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["/bin/zsh","-lc","echo hi > note.txt"]}},"cancel"]}`

// And the question it asked through request_user_input.
const realUserInput = `{"threadId":"t1","turnId":"u1","itemId":"call_1","questions":[{"id":"note_colour","header":"Note colour",
"question":"Which colour should the note be, red or blue?","isOther":true,"isSecret":false,
"options":[{"label":"Red","description":"Make the note red."},{"label":"Blue","description":"Make the note blue."}]}],
"isBlocking":true,"autoResolutionMs":null}`

// sameJSON compares two JSON texts as values: Go writes ">" as \u003e.
func sameJSON(t *testing.T, got []byte, want string) bool {
	t.Helper()
	var a, b any
	if json.Unmarshal(got, &a) != nil || json.Unmarshal([]byte(want), &b) != nil {
		t.Fatalf("not JSON: %s / %s", got, want)
	}
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func request(method, id, params string) *codexRequest {
	return &codexRequest{id: json.RawMessage(id), method: method, params: json.RawMessage(params), answers: map[string][]string{}}
}

// The phone shows what the terminal shows: the command, then Codex's own
// choices in its order, the refusal taking a note.
func TestCodexApprovalReadsAsTheTerminalShowsIt(t *testing.T) {
	shown := request("item/commandExecution/requestApproval", "11", realCommandApproval).question("t1")
	if shown.Prompt != "$ echo hi > note.txt" || shown.Detail != "Would you like to run the following command?" {
		t.Fatalf("prompt %q detail %q", shown.Prompt, shown.Detail)
	}
	want := []protocol.QuestionOption{
		{Key: "1", Label: "Yes, proceed"},
		{Key: "2", Label: "Yes, and don't ask again for commands that start with `echo hi > note.txt`"},
		{Key: "3", Label: codexRefusalLabel, WithText: true},
	}
	if len(shown.Options) != len(want) {
		t.Fatalf("options = %+v", shown.Options)
	}
	for i := range want {
		if shown.Options[i] != want[i] {
			t.Errorf("option %d = %+v, want %+v", i, shown.Options[i], want[i])
		}
	}
	if shown.ID != "codex-request:t1:11:0" {
		t.Fatalf("id = %q", shown.ID)
	}
}

func TestCodexApprovalNamesEveryChoiceCodexOffers(t *testing.T) {
	params := `{"threadId":"t1","command":"curl example.com","reason":"needs the network","availableDecisions":[
"accept","acceptForSession",{"applyNetworkPolicyAmendment":{"network_policy_amendment":{"host":"example.com","action":"allow"}}},
{"applyNetworkPolicyAmendment":{"network_policy_amendment":{"host":"evil.test","action":"deny"}}},"decline","cancel",{"somethingNew":{}}]}`
	shown := request("item/commandExecution/requestApproval", "1", params).question("t1")
	var labels []string
	for _, option := range shown.Options {
		labels = append(labels, option.Key+" "+option.Label)
	}
	want := "1 Yes, proceed|2 Yes, and don't ask again this session|3 Yes, and always allow example.com|" +
		"4 No, and always block evil.test|5 No, continue without it|6 " + codexRefusalLabel
	if got := strings.Join(labels, "|"); got != want {
		t.Fatalf("labels:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(shown.Detail, "Reason: needs the network") {
		t.Fatalf("detail = %q", shown.Detail)
	}
}

func TestCodexEditApprovalWithoutListedChoices(t *testing.T) {
	shown := request("item/fileChange/requestApproval", "2", `{"threadId":"t1","reason":"outside the workspace"}`).question("t1")
	if shown.Prompt != "Would you like to make the following edits?" || len(shown.Options) != 3 ||
		shown.Options[1].Label != "Yes, and don't ask again for these files" || !shown.Options[2].WithText {
		t.Fatalf("shown = %+v", shown)
	}
}

func TestCodexQuestionReadsAsCodexAskedIt(t *testing.T) {
	shown := request("item/tool/requestUserInput", "10", realUserInput).question("t1")
	if shown.Title != "Note colour" || shown.Prompt != "Which colour should the note be, red or blue?" || !shown.Custom || shown.Detail != "" {
		t.Fatalf("shown = %+v", shown)
	}
	if len(shown.Options) != 2 || shown.Options[1] != (protocol.QuestionOption{Key: "Blue", Label: "Blue", Description: "Make the note blue."}) {
		t.Fatalf("options = %+v", shown.Options)
	}
}

func TestCodexApprovalRepliesWithCodexsOwnDecision(t *testing.T) {
	approval := request("item/commandExecution/requestApproval", "11", realCommandApproval)
	id := approval.question("t1").ID

	result, done, note, err := approval.reply("t1", protocol.QuestionAnswer{QuestionID: id, OptionKey: "2"})
	data, _ := json.Marshal(result)
	if err != nil || !done || note != "" ||
		!sameJSON(t, data, `{"decision":{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["/bin/zsh","-lc","echo hi > note.txt"]}}}`) {
		t.Fatalf("reply = %s done=%v note=%q err=%v", data, done, note, err)
	}

	result, done, note, err = approval.reply("t1", protocol.QuestionAnswer{QuestionID: id, OptionKey: "3", Text: " use printf instead "})
	data, _ = json.Marshal(result)
	if err != nil || !done || note != "use printf instead" || string(data) != `{"decision":"cancel"}` {
		t.Fatalf("refusal = %s note=%q err=%v", data, note, err)
	}

	if _, _, _, err := approval.reply("t1", protocol.QuestionAnswer{QuestionID: id, OptionKey: "1", Text: "a note"}); err == nil {
		t.Fatal("a note on an option that takes none was accepted")
	}
	if _, _, _, err := approval.reply("t1", protocol.QuestionAnswer{QuestionID: "codex-request:t1:9:0", OptionKey: "1"}); err == nil {
		t.Fatal("an answer to an older question was accepted")
	}
	if _, _, _, err := approval.reply("t1", protocol.QuestionAnswer{QuestionID: id, OptionKey: "7"}); err == nil {
		t.Fatal("an option Codex did not offer was accepted")
	}
}

// Codex asks several questions in one request; the phone answers them one
// at a time, as the terminal does, and Codex is sent them all together.
func TestCodexAsksSeveralQuestionsOneAtATime(t *testing.T) {
	params := `{"threadId":"t1","questions":[
{"id":"lang","header":"Language","question":"Which language?","isOther":false,"options":[{"label":"Go","description":""},{"label":"Rust","description":""}]},
{"id":"name","header":"Name","question":"What should it be called?","isOther":true,"options":null}]}`
	asking := request("item/tool/requestUserInput", "5", params)
	first := asking.question("t1")
	if first.Detail != "Question 1 of 2" || first.Custom {
		t.Fatalf("first = %+v", first)
	}
	if _, done, _, err := asking.reply("t1", protocol.QuestionAnswer{QuestionID: first.ID, OptionKey: "Go"}); err != nil || done {
		t.Fatalf("after the first: done=%v err=%v", done, err)
	}
	second := asking.question("t1")
	if second.ID == first.ID || second.Prompt != "What should it be called?" || !second.Custom || len(second.Options) != 0 {
		t.Fatalf("second = %+v", second)
	}
	result, done, _, err := asking.reply("t1", protocol.QuestionAnswer{QuestionID: second.ID, Text: "probe"})
	data, _ := json.Marshal(result)
	if err != nil || !done || string(data) != `{"answers":{"lang":{"answers":["Go"]},"name":{"answers":["probe"]}}}` {
		t.Fatalf("result = %s done=%v err=%v", data, done, err)
	}
}

func TestCodexRequestsFollowTheDaemon(t *testing.T) {
	requests := newCodexRequests()
	approval := codexRPCMessage{ID: json.RawMessage("11"), Method: "item/commandExecution/requestApproval", Params: json.RawMessage(realCommandApproval)}
	requests.apply(approval)
	requests.apply(approval) // sent again when the thread is rejoined
	requests.apply(codexRPCMessage{ID: json.RawMessage("12"), Method: "item/permissions/requestApproval", Params: json.RawMessage(`{"threadId":"t1"}`)})
	if _, shown := requests.current("t1"); shown == nil || len(requests.byThread["t1"]) != 1 {
		t.Fatalf("pending = %+v", requests.byThread["t1"])
	}
	requests.apply(notification(t, "serverRequest/resolved", map[string]any{"threadId": "t1", "requestId": 11}))
	if requests.waiting("t1") {
		t.Fatal("still waiting after the daemon resolved it")
	}
	requests.apply(approval)
	requests.apply(notification(t, "thread/closed", map[string]any{"threadId": "t1"}))
	if requests.waiting("t1") {
		t.Fatal("still waiting after the thread closed")
	}
	requests.apply(approval)
	requests.clear()
	if requests.waiting("t1") {
		t.Fatal("still waiting after the connection dropped")
	}
}

// --- through the adapter, against a fake daemon ---

func waitingSource(t *testing.T, flags ...string) (*CodexSource, *fakeCodexConn) {
	t.Helper()
	home := t.TempDir()
	now := time.Now()
	path := writeDaemonRollout(t, home, "t1", "/work/api", now.Add(-time.Hour), now.Add(-time.Second), "task_started")
	src, conn := connectedDaemonSource(t, home, nil, openThread("t1", path, "active", flags...))
	// The sweep that saw the flag joined the thread to be sent its request.
	resume := conn.expect(t, "thread/resume")
	var params map[string]any
	json.Unmarshal(resume.Params, &params)
	if params["threadId"] != "t1" {
		t.Fatalf("joined %v", params["threadId"])
	}
	conn.reply(t, resume, map[string]any{"thread": map[string]any{"id": "t1"}})
	return src, conn
}

func awaitQuestion(t *testing.T, src *CodexSource) protocol.Session {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		session := discovered(t, src)["codex:t1"]
		if session.Question != nil {
			return session
		}
		if time.Now().After(deadline) {
			t.Fatal("the question never showed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func answerAsync(src *CodexSource, answer protocol.QuestionAnswer) <-chan error {
	result := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result <- src.Answer(ctx, "codex:t1", answer)
	}()
	return result
}

// A Codex in the user's own terminal app could not be answered from the
// phone at all. Now the approval shows, the answer goes back as Codex's own
// decision, and Agentman leaves the thread once Codex is not waiting.
func TestCodexApprovalIsShownAndAnsweredThroughTheDaemon(t *testing.T) {
	src, conn := waitingSource(t, "waitingOnApproval")
	conn.send(t, map[string]any{"id": 11, "method": "item/commandExecution/requestApproval", "params": json.RawMessage(realCommandApproval)})

	session := awaitQuestion(t, src)
	if session.State != protocol.StateWaitingInput || session.Question.Prompt != "$ echo hi > note.txt" {
		t.Fatalf("session = %+v %+v", session, session.Question)
	}
	if asked, err := src.CurrentQuestion(context.Background(), "codex:t1"); err != nil || asked == nil || asked.ID != session.Question.ID {
		t.Fatalf("CurrentQuestion = %+v %v", asked, err)
	}

	result := answerAsync(src, protocol.QuestionAnswer{QuestionID: session.Question.ID, OptionKey: "1"})
	answer := conn.next(t)
	var reply struct {
		Decision string `json:"decision"`
	}
	if string(answer.ID) != "11" || json.Unmarshal(answer.Result, &reply) != nil || reply.Decision != "accept" {
		t.Fatalf("answer = %+v", answer)
	}
	if err := waitErr(t, result); err != nil {
		t.Fatal(err)
	}

	// Codex goes back to work: no question, and the thread is left.
	src.live.apply(notification(t, "thread/status/changed", map[string]any{"threadId": "t1", "status": map[string]any{"type": "active", "activeFlags": []string{}}}))
	if session := discovered(t, src)["codex:t1"]; session.Question != nil || session.State != protocol.StateBusy {
		t.Fatalf("after answering: %+v", session)
	}
	conn.expect(t, "thread/unsubscribe")
}

// "No, and tell Codex what to do differently": Codex ends the turn on the
// refusal, and the note is the next message.
func TestCodexRefusalSendsItsNoteOnceTheTurnHasEnded(t *testing.T) {
	src, conn := waitingSource(t, "waitingOnApproval")
	conn.send(t, map[string]any{"id": 11, "method": "item/commandExecution/requestApproval", "params": json.RawMessage(realCommandApproval)})
	session := awaitQuestion(t, src)

	result := answerAsync(src, protocol.QuestionAnswer{QuestionID: session.Question.ID, OptionKey: "3", Text: "use printf instead"})
	answer := conn.next(t)
	if string(answer.Result) != `{"decision":"cancel"}` {
		t.Fatalf("answer = %s", answer.Result)
	}
	conn.reply(t, conn.expect(t, "thread/turns/list"), map[string]any{"data": []any{map[string]any{"id": "u1", "status": "inProgress"}}})
	conn.reply(t, conn.expect(t, "thread/turns/list"), map[string]any{"data": []any{map[string]any{"id": "u1", "status": "interrupted"}}})
	params := expectTurnStart(t, conn)
	if len(params.Input) != 1 || params.Input[0]["text"] != "use printf instead" {
		t.Fatalf("note sent as %+v", params.Input)
	}
	if err := waitErr(t, result); err != nil {
		t.Fatal(err)
	}
}

func TestCodexQuestionIsAnsweredThroughTheDaemon(t *testing.T) {
	src, conn := waitingSource(t, "waitingOnUserInput")
	conn.send(t, map[string]any{"id": 10, "method": "item/tool/requestUserInput", "params": json.RawMessage(realUserInput)})
	session := awaitQuestion(t, src)
	if session.Question.Title != "Note colour" {
		t.Fatalf("question = %+v", session.Question)
	}
	result := answerAsync(src, protocol.QuestionAnswer{QuestionID: session.Question.ID, Text: "green"})
	answer := conn.next(t)
	if string(answer.ID) != "10" || string(answer.Result) != `{"answers":{"note_colour":{"answers":["green"]}}}` {
		t.Fatalf("answer = %s %s", answer.ID, answer.Result)
	}
	if err := waitErr(t, result); err != nil {
		t.Fatal(err)
	}
}

// Answered in the terminal instead: the daemon says so and the phone's
// question goes away.
func TestCodexQuestionAnsweredInTheTerminalGoesAway(t *testing.T) {
	src, conn := waitingSource(t, "waitingOnApproval")
	conn.send(t, map[string]any{"id": 11, "method": "item/commandExecution/requestApproval", "params": json.RawMessage(realCommandApproval)})
	awaitQuestion(t, src)
	conn.send(t, map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"threadId": "t1", "requestId": 11}})
	deadline := time.Now().Add(5 * time.Second)
	for src.daemonQuestion("t1") != nil {
		if time.Now().After(deadline) {
			t.Fatal("the question stayed after the terminal answered it")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
