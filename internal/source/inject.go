package source

import (
	"context"
	"fmt"
	"sync"

	"github.com/lenajeremy/agentman/internal/protocol"
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

// SetPending gives the Codex source a fallback queue.
func (s *CodexSource) SetPending(queue *PendingQueue) { s.pending = queue }

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

	if s.pending == nil {
		return protocol.InjectNone, fmt.Errorf(
			"source: this session cannot receive messages — start it with `am codex` to enable sending")
	}
	s.pending.Add(sessionID, text)
	return protocol.InjectHook, nil
}
