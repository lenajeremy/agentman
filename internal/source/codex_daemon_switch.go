package source

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Switching a Codex session's mode and model, and compacting it, through the
// shared daemon. thread/settings/update changes what the thread's next turns
// use, the same as choosing "this session only" in Codex's terminal: nothing
// becomes the default for other sessions. Unlike driving the terminal's
// pickers it works while Codex is working (the change takes effect at the next
// turn) and for a session that has no pane at all.

// switchThroughDaemon applies settings to the session's thread. ok is false
// when the daemon does not have the session open, and the terminal path is
// used as before.
func (s *CodexSource) switchThroughDaemon(ctx context.Context, sessionID string, settings func(codexDaemonThread) map[string]any) (bool, error) {
	s.mu.RLock()
	session, known := s.sessions[sessionID]
	s.mu.RUnlock()
	if !known {
		return true, fmt.Errorf("source: unknown codex session %q", sessionID)
	}
	thread := s.daemonThreadOf(session)
	server := s.connection()
	if thread == "" || server == nil {
		return false, nil
	}
	threads, _ := s.liveThreads()
	params := settings(threads[thread])
	params["threadId"] = thread
	if err := server.call(ctx, "thread/settings/update", params, nil); err != nil {
		if codexFallBack(err) {
			return false, nil // the terminal's pickers, as before
		}
		return true, fmt.Errorf("source: Codex did not take the change: %w", err)
	}
	return true, nil
}

// setModeThroughDaemon switches between Codex's default and plan modes.
func (s *CodexSource) setModeThroughDaemon(ctx context.Context, sessionID, mode string) (bool, error) {
	if !slices.Contains(codexModes, mode) {
		return true, fmt.Errorf("source: Codex has no %q mode", mode)
	}
	ok, err := s.switchThroughDaemon(ctx, sessionID, func(thread codexDaemonThread) map[string]any {
		// A collaboration mode names its model and reasoning effort; the
		// thread's own are kept, or the effort would fall back to the
		// model's default. Null instructions are the mode's built-in ones.
		var effort any
		if thread.Effort != "" {
			effort = thread.Effort
		}
		return map[string]any{"collaborationMode": map[string]any{
			"mode": mode,
			"settings": map[string]any{
				"model": thread.Model, "reasoning_effort": effort, "developer_instructions": nil,
			},
		}}
	})
	if ok && err == nil {
		s.noteThreadSwitch(sessionID, func(thread *codexDaemonThread) { thread.Mode = mode })
	}
	return ok, err
}

// setModelThroughDaemon switches the model of the session's next turns.
func (s *CodexSource) setModelThroughDaemon(ctx context.Context, sessionID, model string) (bool, error) {
	listed := false
	for _, entry := range s.codexModels() {
		listed = listed || entry.slug == model
	}
	if !listed {
		return true, fmt.Errorf("source: %q is not a model Codex lists", model)
	}
	ok, err := s.switchThroughDaemon(ctx, sessionID, func(codexDaemonThread) map[string]any {
		return map[string]any{"model": model}
	})
	if ok && err == nil {
		s.noteThreadSwitch(sessionID, func(thread *codexDaemonThread) { thread.Model = model })
	}
	return ok, err
}

// noteThreadSwitch shows a switch at once, before the daemon's own
// notification of it arrives.
func (s *CodexSource) noteThreadSwitch(sessionID string, change func(*codexDaemonThread)) {
	s.mu.RLock()
	session := s.sessions[sessionID]
	s.mu.RUnlock()
	live := s.liveTable()
	if live == nil {
		return
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	if thread, ok := live.threads[session.meta.NativeID]; ok {
		change(&thread)
		live.threads[thread.ID] = thread
	}
}

func (s *CodexSource) liveTable() *codexLiveThreads {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	return s.live
}

// codexCompactCommand is the slash command that compacts the conversation,
// which the daemon does directly.
func codexCompactCommand(text string) bool {
	return strings.TrimSpace(text) == "/compact"
}
