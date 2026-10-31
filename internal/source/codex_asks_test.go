package source

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// The records Codex 0.166 wrote when asked "can you ask me a question using
// the question tool" outside plan mode, as captured on 2026-10-31.
func appendAsyncQuestion(t *testing.T, path, callID string, questions ...obj) {
	t.Helper()
	now := time.Now().Format(time.RFC3339Nano)
	appendJSONL(t, path, obj{"timestamp": now, "type": "response_item", "payload": obj{
		"type": "function_call", "name": "request_user_input_async", "call_id": callID, "arguments": `{"questions":[]}`}})
	appendJSONL(t, path, obj{"timestamp": now, "type": "event_msg", "payload": obj{"type": "item_completed", "item": obj{
		"type": "AgentMessage", "id": callID, "delivery": "async", "questions": questions, "phase": "final_answer",
		"content": []any{obj{"type": "Text", "text": "the question, as words"}}}}})
	appendJSONL(t, path, obj{"timestamp": now, "type": "response_item", "payload": obj{
		"type": "function_call_output", "call_id": callID, "output": `{"accepted":true}`}})
	appendJSONL(t, path, codexAgentMessage("msg_after", "Question sent—pick an answer in the question box."))
	appendJSONL(t, path, obj{"timestamp": now, "type": "event_msg", "payload": obj{"type": "task_complete"}})
}

func appendUserMessage(t *testing.T, path, text string) {
	t.Helper()
	appendJSONL(t, path, obj{"timestamp": time.Now().Format(time.RFC3339Nano), "type": "event_msg", "payload": obj{
		"type": "item_completed", "item": obj{"type": "UserMessage", "id": "u-" + text[:3],
			"content": []any{obj{"type": "text", "text": text}}}}})
}

var structQuestion = obj{"title": "A struct has 10 bytes of field data and alignment 8. How much space does each element occupy in an array?",
	"options": []string{"16 bytes", "10 bytes", "24 bytes"}}

func TestCodexFindsTheQuestionCodexAskedWithoutWaiting(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeDaemonRollout(t, home, "t1", "/work/api", now.Add(-time.Hour), now.Add(-time.Minute), "task_complete")
	asks := newCodexAsks()
	if asks.pending(path) != nil {
		t.Fatal("a question before any was asked")
	}
	appendAsyncQuestion(t, path, "call_549", structQuestion)
	ask := asks.pending(path)
	if ask == nil || ask.callID != "call_549" || len(ask.questions) != 1 || len(ask.questions[0].Options) != 3 {
		t.Fatalf("ask = %+v", ask)
	}
	// Any message after it answers or dismisses it.
	appendUserMessage(t, path, "never mind, carry on")
	if ask := asks.pending(path); ask != nil {
		t.Fatalf("still pending after a message: %+v", ask)
	}
}

func TestCodexReplyIsTheFormCodexsAppSends(t *testing.T) {
	ask := &codexAsk{callID: "call_8h", questions: []codexAskQuestion{{Title: "Should I use Puppeteer?"}}}
	want := "<send_user_message_question_reply>\n" +
		`[{"answer":"Yes, use Puppeteer","question":"Should I use Puppeteer?","questionItemId":"[\"request_user_input_async\",\"call_8h\",0]"}]` +
		"\n</send_user_message_question_reply>"
	got := codexAskReply(ask, []string{"Yes, use Puppeteer"})
	if got != want {
		t.Fatalf("reply:\n%s\nwant:\n%s", got, want)
	}
	// And the rollout parser shows it as the answer alone.
	if text := onlyRowText(t, got); text != "Yes, use Puppeteer" {
		t.Fatalf("shown as %q", text)
	}
}

func onlyRowText(t *testing.T, text string) string {
	t.Helper()
	home := t.TempDir()
	now := time.Now()
	path := writeDaemonRollout(t, home, "t9", "/work/api", now.Add(-time.Hour), now, "task_complete")
	appendUserMessage(t, path, text)
	src := daemonSource(t, home, false)
	if _, err := src.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	page, err := src.Page(context.Background(), "codex:t9", "", 1)
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("page = %+v %v", page, err)
	}
	return page.Messages[0].Text
}

func askingSource(t *testing.T, questions ...obj) (*CodexSource, *fakeCodexConn) {
	t.Helper()
	home := t.TempDir()
	now := time.Now()
	path := writeDaemonRollout(t, home, "t1", "/work/api", now.Add(-time.Hour), now.Add(-time.Minute), "task_complete")
	appendAsyncQuestion(t, path, "call_549", questions...)
	return connectedDaemonSource(t, home, nil, openThread("t1", path, "idle"))
}

func TestCodexShowsAQuestionCodexAskedWithoutWaiting(t *testing.T) {
	src, conn := askingSource(t, structQuestion)
	session := discovered(t, src)["codex:t1"]
	if session.State != protocol.StateWaitingInput || session.Question == nil {
		t.Fatalf("session = %+v", session)
	}
	q := session.Question
	if q.Prompt != structQuestion["title"] || !q.Custom || len(q.Options) != 3 || q.Options[0] != (protocol.QuestionOption{Key: "16 bytes", Label: "16 bytes"}) {
		t.Fatalf("question = %+v", q)
	}

	result := answerAsync(src, protocol.QuestionAnswer{QuestionID: q.ID, OptionKey: "16 bytes"})
	params := expectTurnStart(t, conn)
	want := codexAskReply(&codexAsk{callID: "call_549", questions: []codexAskQuestion{{Title: structQuestion["title"].(string)}}}, []string{"16 bytes"})
	if params.ThreadID != "t1" || len(params.Input) != 1 || params.Input[0]["text"] != want {
		t.Fatalf("sent %+v", params)
	}
	if err := waitErr(t, result); err != nil {
		t.Fatal(err)
	}
}

func TestCodexTakesWordsForAQuestionCodexAskedWithoutWaiting(t *testing.T) {
	src, conn := askingSource(t, structQuestion)
	q := discovered(t, src)["codex:t1"].Question
	result := answerAsync(src, protocol.QuestionAnswer{QuestionID: q.ID, Text: "it depends on the field order"})
	params := expectTurnStart(t, conn)
	if text, _ := params.Input[0]["text"].(string); !containsAll(text, `"answer":"it depends on the field order"`, `\"call_549\",0]`) {
		t.Fatalf("sent %q", text)
	}
	if err := waitErr(t, result); err != nil {
		t.Fatal(err)
	}
	if err := waitErr(t, answerAsync(src, protocol.QuestionAnswer{QuestionID: "codex-ask:t1:call_old:0", OptionKey: "16 bytes"})); err == nil {
		t.Fatal("an answer to another question was taken")
	}
}

func TestCodexAsksSeveralWithoutWaitingOneAtATime(t *testing.T) {
	src, conn := askingSource(t, obj{"title": "Language?", "options": []string{"Go", "Rust"}}, obj{"title": "Name?", "options": nil})
	first := discovered(t, src)["codex:t1"].Question
	if first.Detail != "Question 1 of 2" {
		t.Fatalf("first = %+v", first)
	}
	if err := waitErr(t, answerAsync(src, protocol.QuestionAnswer{QuestionID: first.ID, OptionKey: "Go"})); err != nil {
		t.Fatal(err)
	}
	conn.quiet(t) // nothing sent until both are answered
	second := discovered(t, src)["codex:t1"].Question
	if second.Prompt != "Name?" || second.ID == first.ID || len(second.Options) != 0 {
		t.Fatalf("second = %+v", second)
	}
	result := answerAsync(src, protocol.QuestionAnswer{QuestionID: second.ID, Text: "probe"})
	params := expectTurnStart(t, conn)
	if text, _ := params.Input[0]["text"].(string); !containsAll(text, `"answer":"Go"`, `"answer":"probe"`, `\"call_549\",1]`) {
		t.Fatalf("sent %q", text)
	}
	if err := waitErr(t, result); err != nil {
		t.Fatal(err)
	}
}

// A session the daemon does not have keeps to its terminal, as before.
func TestCodexLeavesQuestionsWithoutTheDaemonToTheTerminal(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeDaemonRollout(t, home, "t1", "/work/api", now.Add(-time.Hour), now.Add(-time.Minute), "task_complete")
	appendAsyncQuestion(t, path, "call_549", structQuestion)
	src := daemonSource(t, home, false)
	if session := discovered(t, src)["codex:t1"]; session.Question != nil {
		t.Fatalf("question = %+v", session.Question)
	}
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}
