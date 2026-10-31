package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Codex's questions that do not wait.
//
// Outside plan mode Codex asks with request_user_input_async: the question
// rides on an agent message marked delivery "async", the tool answers
// {"accepted": true} at once, and the turn ends. The client is meant to show
// a question box and send the answer as the next message. A Codex terminal
// older than its daemon shows no box at all (Codex 0.164 on a 0.166 daemon,
// 2026-10-31: the transcript said "pick an answer in the question box" over
// an empty prompt), so Agentman shows the question itself, from the rollout,
// and answers the way Codex's own app does:
//
//	<send_user_message_question_reply>
//	[{"answer":"…","question":"…","questionItemId":"[\"request_user_input_async\",\"call_…\",0]"}]
//	</send_user_message_question_reply>
//
// A question is pending until a user message follows it; a message sent
// instead of answering dismisses it, as in Codex. Only a session the shared
// daemon has open is answered this way: the answer is a turn/start, never
// typed.

// codexAskScanBytes bounds how much of a rollout's end is read to find the
// latest question: the question is the last thing of its turn, so it is near
// the end whenever it is still pending.
const codexAskScanBytes = 512 << 10

// codexAsk is a question Codex asked that has no answer yet.
type codexAsk struct {
	callID    string
	questions []codexAskQuestion
}

type codexAskQuestion struct {
	Title   string   `json:"title"`
	Options []string `json:"options"`
}

// codexAsks remembers each rollout's pending question, read again only when
// the rollout changes, and the answers given so far to one with several
// questions.
type codexAsks struct {
	mu      sync.Mutex
	read    map[string]codexAskRead
	answers map[string][]string // call id -> answers so far
}

type codexAskRead struct {
	version codexFileVersion
	ask     *codexAsk
}

func newCodexAsks() *codexAsks {
	return &codexAsks{read: map[string]codexAskRead{}, answers: map[string][]string{}}
}

// pending is the rollout's unanswered question, or nil.
func (a *codexAsks) pending(path string) *codexAsk {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	a.mu.Lock()
	if cached, ok := a.read[path]; ok && cached.version.matches(info) {
		a.mu.Unlock()
		return cached.ask
	}
	a.mu.Unlock()
	ask := readCodexAsk(path, info.Size())
	a.mu.Lock()
	defer a.mu.Unlock()
	// The question this rollout had, answered or dismissed since: whatever
	// was collected for it is moot.
	if before := a.read[path].ask; before != nil && (ask == nil || ask.callID != before.callID) {
		delete(a.answers, before.callID)
	}
	a.read[path] = codexAskRead{version: codexVersion(info), ask: ask}
	return ask
}

// forget drops rollouts no longer read.
func (a *codexAsks) forget(live map[string]bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for path := range a.read {
		if !live[path] {
			delete(a.read, path)
		}
	}
}

// readCodexAsk walks a rollout's end backwards to the latest user message or
// question, whichever is later.
func readCodexAsk(path string, size int64) *codexAsk {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	start := max(size-codexAskScanBytes, 0)
	buf := make([]byte, size-start)
	if _, err := file.ReadAt(buf, start); err != nil && err != io.EOF {
		return nil
	}
	lines := bytes.Split(buf, []byte{'\n'})
	if start > 0 && len(lines) > 0 {
		lines = lines[1:] // the first is cut
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if !bytes.Contains(line, []byte(`"item_completed"`)) {
			continue
		}
		var record struct {
			Type    string `json:"type"`
			Payload struct {
				Type string `json:"type"`
				Item struct {
					Type      string             `json:"type"`
					ID        string             `json:"id"`
					Delivery  string             `json:"delivery"`
					Questions []codexAskQuestion `json:"questions"`
				} `json:"item"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &record) != nil || record.Type != "event_msg" || record.Payload.Type != "item_completed" {
			continue
		}
		item := record.Payload.Item
		switch {
		case item.Type == "UserMessage":
			return nil
		case item.Type == "AgentMessage" && item.Delivery == "async" && len(item.Questions) > 0 && item.ID != "":
			return &codexAsk{callID: item.ID, questions: item.Questions}
		}
	}
	return nil
}

// question is what the apps show: the first question not yet answered.
func (a *codexAsks) question(thread string, ask *codexAsk) *protocol.Question {
	a.mu.Lock()
	index := len(a.answers[ask.callID])
	a.mu.Unlock()
	if index >= len(ask.questions) {
		return nil
	}
	asked := ask.questions[index]
	shown := &protocol.Question{
		ID:     fmt.Sprintf("codex-ask:%s:%s:%d", thread, ask.callID, index),
		Prompt: asked.Title,
		// Codex takes any words as the answer, as its own box does.
		Custom: true,
	}
	if len(ask.questions) > 1 {
		shown.Detail = fmt.Sprintf("Question %d of %d", index+1, len(ask.questions))
	}
	for _, option := range asked.Options {
		shown.Options = append(shown.Options, protocol.QuestionOption{Key: option, Label: option})
	}
	return shown
}

// codexAskReply is the message that answers a question, in the form Codex's
// own app sends.
func codexAskReply(ask *codexAsk, answers []string) string {
	type reply struct {
		Answer         string `json:"answer"`
		Question       string `json:"question"`
		QuestionItemID string `json:"questionItemId"`
	}
	replies := make([]reply, len(answers))
	for i, answer := range answers {
		item, _ := json.Marshal([]any{"request_user_input_async", ask.callID, i})
		replies[i] = reply{Answer: answer, Question: ask.questions[i].Title, QuestionItemID: string(item)}
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(replies)
	return "<send_user_message_question_reply>\n" + strings.TrimSpace(body.String()) + "\n</send_user_message_question_reply>"
}

// pendingAsk is the question a session's thread is waiting on, from its
// rollout, when the daemon has the session open.
func (s *CodexSource) pendingAsk(session codexSession) (string, *codexAsk) {
	thread := s.daemonThreadOf(session)
	if thread == "" || s.asks == nil {
		return "", nil
	}
	return thread, s.asks.pending(session.transcript)
}

// askQuestion is the question card for a session's pending question, or nil.
func (s *CodexSource) askQuestion(session codexSession) *protocol.Question {
	thread, ask := s.pendingAsk(session)
	if ask == nil {
		return nil
	}
	return s.asks.question(thread, ask)
}

// answerAsk answers a session's pending question. handled is false when
// there is none.
func (s *CodexSource) answerAsk(ctx context.Context, session codexSession, answer protocol.QuestionAnswer) (bool, error) {
	thread, ask := s.pendingAsk(session)
	server := s.connection()
	if ask == nil || server == nil {
		return false, nil
	}
	shown := s.asks.question(thread, ask)
	if shown == nil || answer.QuestionID != shown.ID {
		return true, errors.New("source: that question is no longer current; refresh the session")
	}
	given := strings.TrimSpace(answer.Text)
	if given == "" {
		given = answer.OptionKey
		if !questionHasOption(shown, given) {
			return true, errors.New("source: that question or option is no longer current; refresh the session")
		}
	}
	if len(answer.Options) > 0 {
		return true, errors.New("source: choose one listed option or provide one custom answer")
	}
	s.asks.mu.Lock()
	answers := append(s.asks.answers[ask.callID], given)
	s.asks.answers[ask.callID] = answers
	s.asks.mu.Unlock()
	if len(answers) < len(ask.questions) {
		return true, nil // the next question shows
	}
	input := []any{map[string]any{"type": "text", "text": codexAskReply(ask, answers), "text_elements": []any{}}}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := server.call(callCtx, "turn/start", map[string]any{"threadId": thread, "input": input}, nil); err != nil {
		s.asks.mu.Lock()
		s.asks.answers[ask.callID] = answers[:len(answers)-1]
		s.asks.mu.Unlock()
		return true, fmt.Errorf("source: Codex did not take the answer: %w", err)
	}
	return true, nil
}
