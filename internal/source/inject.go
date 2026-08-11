package source

import (
	"context"
	"fmt"
	"sync"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// PendingQueue holds messages that could not be delivered immediately.
//
// A session started outside the tmux wrapper has no live input channel, so the
// only way in is to wait for its next Stop hook and hand the text back as the
// reason the agent should keep going. That means delivery happens between
// turns, never during one — and the CLI can discard the injection, which is
// why the app reports these as "queued" rather than sent.
type PendingQueue struct {
	mu       sync.Mutex
	messages map[string][]string
}

// NewPendingQueue creates an empty queue.
func NewPendingQueue() *PendingQueue {
	return &PendingQueue{messages: map[string][]string{}}
}

// Add queues a message for a session.
func (q *PendingQueue) Add(sessionID, text string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	// Cap the backlog: if the agent never stops, an impatient user could
	// otherwise queue a hundred messages that all arrive at once.
	const maxPending = 10
	queued := q.messages[sessionID]
	if len(queued) >= maxPending {
		queued = queued[1:]
	}
	q.messages[sessionID] = append(queued, text)
}

// Take removes and returns everything queued for a session.
func (q *PendingQueue) Take(sessionID string) []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	queued := q.messages[sessionID]
	delete(q.messages, sessionID)
	return queued
}

// Len reports how many messages are waiting for a session.
func (q *PendingQueue) Len(sessionID string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages[sessionID])
}

// SetPending gives a source a queue for messages it cannot deliver live.
func (s *ClaudeSource) SetPending(queue *PendingQueue) { s.pending = queue }

// Inject implements Injector for Claude Code.
//
// Prefers tmux, which types into the session exactly as the user would and
// works even mid-turn. Falls back to the pending queue, whose delivery is
// best-effort — the returned mode tells the caller which happened so the UI
// can be honest about it.
func (s *ClaudeSource) Inject(ctx context.Context, sessionID, text string) (protocol.InjectMode, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return protocol.InjectNone, fmt.Errorf("source: unknown claude session %q", sessionID)
	}

	if session.tmuxName != "" {
		if err := tmux.Send(ctx, session.tmuxName, text); err != nil {
			return protocol.InjectNone, err
		}
		return protocol.InjectTmux, nil
	}

	if s.pending == nil {
		return protocol.InjectNone, fmt.Errorf(
			"source: this session cannot receive messages — start it with `am claude` to enable sending")
	}
	s.pending.Add(sessionID, text)
	return protocol.InjectHook, nil
}

// Interrupt stops a running turn, where the session supports it.
func (s *ClaudeSource) Interrupt(ctx context.Context, sessionID string) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown claude session %q", sessionID)
	}
	if session.tmuxName == "" {
		return fmt.Errorf("source: only sessions started with `am claude` can be interrupted")
	}
	return tmux.Interrupt(ctx, session.tmuxName)
}

// Inject implements Injector for Codex.
func (s *CodexSource) Inject(ctx context.Context, sessionID, text string) (protocol.InjectMode, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return protocol.InjectNone, fmt.Errorf("source: unknown codex session %q", sessionID)
	}

	if session.tmuxName != "" {
		if err := tmux.Send(ctx, session.tmuxName, text); err != nil {
			return protocol.InjectNone, err
		}
		return protocol.InjectTmux, nil
	}

	// Codex's supported notify callback is fire-and-forget: stdout is discarded
	// and it cannot feed a blocking decision or queued prompt back into the
	// turn. Advertising hook delivery here made the app report "queued" for a
	// message that could never arrive.
	return protocol.InjectNone, fmt.Errorf(
		"source: this session cannot receive messages — start it with `am codex` to enable sending")
}

// detectQuestion reads a pane and reports any decision the agent is blocked on.
//
// Runs on every discovery sweep for every tmux-backed session, so it must stay
// cheap: one capture-pane per session per second, parsed in memory. Failures
// are silent — a pane that cannot be read simply means no question, which is
// the same conclusion as an agent that is working normally.
func detectQuestion(ctx context.Context, tmuxName string) *protocol.Question {
	pane, err := tmux.Capture(ctx, tmuxName)
	if err != nil {
		return nil
	}
	found := question.Detect(pane)
	if found == nil {
		return nil
	}
	options := make([]protocol.QuestionOption, 0, len(found.Options))
	for _, option := range found.Options {
		options = append(options, protocol.QuestionOption{
			Key:      option.Key,
			Label:    option.Label,
			Selected: option.Selected,
		})
	}
	return &protocol.Question{
		Prompt:  found.Prompt,
		Title:   found.Title,
		Detail:  found.Detail,
		Options: options,
	}
}

// Answer selects an option in a question a Claude session is showing.
func (s *ClaudeSource) Answer(ctx context.Context, sessionID string, answer protocol.QuestionAnswer) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown claude session %q", sessionID)
	}
	if session.tmuxName == "" {
		return fmt.Errorf("source: only sessions started with `am claude` can be answered")
	}
	if len(answer.Options) > 0 || answer.Text != "" {
		return fmt.Errorf("source: this terminal question only accepts one listed option")
	}
	if err := validateCurrentQuestion(ctx, session.tmuxName, session.meta.Question, answer.OptionKey); err != nil {
		return err
	}
	return tmux.Answer(ctx, session.tmuxName, answer.OptionKey)
}

// Answer selects an option in a question a Codex session is showing.
func (s *CodexSource) Answer(ctx context.Context, sessionID string, answer protocol.QuestionAnswer) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown codex session %q", sessionID)
	}
	if session.tmuxName == "" {
		return fmt.Errorf("source: only sessions started with `am codex` can be answered")
	}
	if len(answer.Options) > 0 || answer.Text != "" {
		return fmt.Errorf("source: this terminal question only accepts one listed option")
	}
	if err := validateCurrentQuestion(ctx, session.tmuxName, session.meta.Question, answer.OptionKey); err != nil {
		return err
	}
	return tmux.Answer(ctx, session.tmuxName, answer.OptionKey)
}

// validateCurrentQuestion prevents a delayed phone tap from being typed into
// an unrelated prompt. The pane is re-read immediately before the keystroke;
// both the question identity and the selected key must still match what the
// app was shown during discovery.
func validateCurrentQuestion(
	ctx context.Context,
	tmuxName string,
	shown *protocol.Question,
	optionKey string,
) error {
	if shown == nil || !questionHasOption(shown, optionKey) {
		return fmt.Errorf("source: that question or option is no longer current; refresh the session")
	}
	current := detectQuestion(ctx, tmuxName)
	if current == nil || !sameQuestion(shown, current) || !questionHasOption(current, optionKey) {
		return fmt.Errorf("source: that question is no longer on screen; refresh the session")
	}
	return nil
}

func questionHasOption(question *protocol.Question, key string) bool {
	if question == nil || key == "" {
		return false
	}
	for _, option := range question.Options {
		if option.Key == key {
			return true
		}
	}
	return false
}

func sameQuestion(left, right *protocol.Question) bool {
	if left == nil || right == nil || left.Prompt != right.Prompt || left.Title != right.Title ||
		left.Detail != right.Detail || left.Multiple != right.Multiple || left.Custom != right.Custom ||
		len(left.Options) != len(right.Options) {
		return false
	}
	for i := range left.Options {
		if left.Options[i].Key != right.Options[i].Key || left.Options[i].Label != right.Options[i].Label {
			return false
		}
	}
	return true
}
