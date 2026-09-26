package source

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func cursorACPFake(t *testing.T) (*CursorACPSource, *cursorACPState, string, *bufio.Reader) {
	t.Helper()
	s, err := NewCursorACPSource(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	native := "12345678-1234-1234-1234-123456789abc"
	id := cursorACPPrefix + native
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	st := &cursorACPState{
		record: cursorACPRecord{NativeID: native, Cwd: t.TempDir(), StartedAt: time.Now().UnixMilli(), LastActivityAt: time.Now().UnixMilli()},
		client: &cursorACPClient{stdin: w}, busy: true,
	}
	s.sessions[id] = st
	return s, st, id, bufio.NewReader(r)
}

func TestCursorACPPermissionStateAndAnswer(t *testing.T) {
	s, st, id, replies := cursorACPFake(t)
	s.handle(st, cursorACPEnvelope{ID: json.RawMessage(`7`), Method: "session/request_permission", Params: json.RawMessage(`{
		"toolCall":{"title":"Run pwd","rawInput":{"command":"pwd"}},
		"options":[{"optionId":"allow-once","name":"Allow once"},{"optionId":"reject-once","name":"Reject"}]}`)})
	sessions, err := s.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].State != protocol.StateWaitingInput || len(sessions[0].Question.Options) != 2 {
		t.Fatalf("permission was not surfaced: %+v", sessions)
	}
	q := sessions[0].Question
	if err := s.Answer(context.Background(), id, protocol.QuestionAnswer{QuestionID: q.ID, OptionKey: "allow-once"}); err != nil {
		t.Fatal(err)
	}
	line, err := replies.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(line), `"optionId":"allow-once"`) {
		t.Fatalf("wrong ACP reply: %s", line)
	}
	sessions, _ = s.Discover(context.Background())
	if sessions[0].State != protocol.StateBusy || sessions[0].Question != nil {
		t.Fatalf("question did not clear: %+v", sessions[0])
	}
	if err := s.Answer(context.Background(), id, protocol.QuestionAnswer{QuestionID: q.ID, OptionKey: "reject-once"}); err == nil {
		t.Fatal("stale permission answer was accepted")
	}
}

func TestCursorACPInterruptUsesProtocolCancellation(t *testing.T) {
	s, _, id, replies := cursorACPFake(t)
	if err := s.Interrupt(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	line, err := replies.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(line), `"method":"session/cancel"`) {
		t.Fatalf("wrong ACP cancellation: %s", line)
	}
}

func TestCursorACPMultipleQuestionsAreAnsweredInOrder(t *testing.T) {
	s, st, id, replies := cursorACPFake(t)
	s.handle(st, cursorACPEnvelope{ID: json.RawMessage(`9`), Method: "cursor/ask_question", Params: json.RawMessage(`{
		"title":"Choose settings","questions":[
		{"id":"q1","prompt":"Mode?","options":[{"id":"fast","label":"Fast"}]},
		{"id":"q2","prompt":"Color?","options":[{"id":"blue","label":"Blue"}]}]}`)})
	first, _ := s.CurrentQuestion(context.Background(), id)
	if first == nil || first.Prompt != "Mode?" {
		t.Fatalf("wrong first question: %+v", first)
	}
	if err := s.Answer(context.Background(), id, protocol.QuestionAnswer{QuestionID: first.ID, OptionKey: "fast"}); err != nil {
		t.Fatal(err)
	}
	second, _ := s.CurrentQuestion(context.Background(), id)
	if second == nil || second.Prompt != "Color?" || second.ID == first.ID {
		t.Fatalf("wrong second question: %+v", second)
	}
	if err := s.Answer(context.Background(), id, protocol.QuestionAnswer{QuestionID: second.ID, OptionKey: "blue"}); err != nil {
		t.Fatal(err)
	}
	line, err := replies.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(line), `"questionId":"q1"`) || !strings.Contains(string(line), `"questionId":"q2"`) {
		t.Fatalf("answers missing from ACP response: %s", line)
	}
}

func TestCursorACPReadsChangesWrittenByAnotherProcess(t *testing.T) {
	s, st, id, _ := cursorACPFake(t)
	if err := s.save(st); err != nil {
		t.Fatal(err)
	}
	other, err := NewCursorACPSource(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	s.handle(st, cursorACPEnvelope{Method: "session/update", Params: json.RawMessage(`{
		"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"live reply"}}}`)})
	page, err := other.Page(context.Background(), id, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 1 || page.Messages[0].Text != "live reply" {
		t.Fatalf("other process did not see persisted stream: %+v", page)
	}
	lock, err := s.lockTurn(st.record.NativeID)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseCursorACPLock(lock)
	sessions, err := other.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].State != protocol.StateBusy {
		t.Fatalf("other process did not see active turn: %+v", sessions)
	}
}

func TestCursorACPSessionTitleUpdates(t *testing.T) {
	s, st, _, _ := cursorACPFake(t)
	s.handle(st, cursorACPEnvelope{Method: "session/update", Params: json.RawMessage(`{
		"update":{"sessionUpdate":"session_info_update","title":"Investigate Cursor streaming"}}`)})
	sessions, err := s.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Name != "Investigate Cursor streaming" {
		t.Fatalf("title update was not surfaced: %+v", sessions)
	}
}

func TestCursorACPDoesNotAcknowledgeFailedPromptWrite(t *testing.T) {
	s, st, _, _ := cursorACPFake(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	_ = w.Close()
	client := &cursorACPClient{stdin: w, pending: make(map[int]chan cursorACPEnvelope), closed: make(chan struct{})}
	lock, err := s.lockTurn(st.record.NativeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.begin(st, client, lock, "should not appear"); err == nil {
		t.Fatal("broken ACP pipe was acknowledged")
	}
	page, err := s.Page(context.Background(), cursorACPPrefix+st.record.NativeID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 0 {
		t.Fatalf("failed prompt entered transcript: %+v", page.Messages)
	}
}
