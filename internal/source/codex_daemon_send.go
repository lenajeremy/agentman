package source

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Sending to Codex and stopping it through the shared daemon, rather than by
// typing into its terminal.
//
// A message goes in as a turn/start: it starts a turn when Codex is idle, and
// joins the turn under way when it is working, as Enter does in the terminal
// (checked against Codex 0.166: the reply named the same turn, and the turn
// took the new message into account). Nothing is typed, so a terminal
// mid-menu, in an odd editing state or not in tmux at all makes no difference:
// a Codex the user started in their own terminal app can now be messaged.
//
// Codex's terminal reads some messages itself and never sends them to the
// model as written: a slash command, a "!" shell command, a "$skill". While
// the session has a pane those are still typed there.

// errCodexNeedsTerminal is a message only Codex's terminal can take, for a
// session that has none Agentman can type into.
var errCodexNeedsTerminal = errors.New(
	"source: slash commands, ! commands and $skills need Codex's terminal; start the session with `am codex` to send them")

// codexTerminalOnly reports whether Codex's terminal would read the message
// itself instead of sending it to the model.
func codexTerminalOnly(text string) bool {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "!") {
		return true
	}
	for _, word := range strings.Fields(trimmed) {
		if len(word) > 1 && word[0] == '$' && unicode.IsLetter(rune(word[1])) {
			return true
		}
	}
	return false
}

// sendThroughDaemon delivers a message as a turn input. ok is false when
// the session is not one the daemon has open, or the daemon could not take
// it, and the caller falls back to the terminal.
func (s *CodexSource) sendThroughDaemon(ctx context.Context, sessionID string, session codexSession, text string, images []string) (bool, error) {
	thread := s.daemonThreadOf(session)
	server := s.connection()
	if thread == "" || server == nil {
		return false, nil
	}
	// The daemon compacts directly, pane or not.
	if codexCompactCommand(text) && len(images) == 0 {
		if err := server.call(ctx, "thread/compact/start", map[string]any{"threadId": thread}, nil); err != nil {
			if codexFallBack(err) && session.tmuxName != "" {
				return false, nil
			}
			return true, fmt.Errorf("source: Codex did not compact: %w", err)
		}
		return true, nil
	}
	if codexTerminalOnly(text) {
		if session.tmuxName != "" {
			return false, nil
		}
		return true, errCodexNeedsTerminal
	}
	input := make([]any, 0, len(images)+1)
	for _, path := range images {
		input = append(input, map[string]any{"type": "localImage", "path": path})
	}
	if text != "" {
		input = append(input, map[string]any{"type": "text", "text": text, "text_elements": []any{}})
	}
	if len(input) == 0 {
		return true, nil
	}
	if err := server.call(ctx, "turn/start", map[string]any{"threadId": thread, "input": input}, nil); err != nil {
		if codexFallBack(err) && session.tmuxName != "" {
			return false, nil
		}
		return true, fmt.Errorf("source: Codex did not take the message: %w", err)
	}
	// Sent into a running turn, it waits for the step under way, as one
	// typed there does; see heldSends. Matched on the words alone: how the
	// rollout records pictures sent this way is the daemon's business.
	if session.meta.State == protocol.StateBusy || session.meta.State == protocol.StateWaitingInput {
		s.held.add(sessionID, session.transcript, text)
	}
	return true, nil
}

// Interrupt implements Interrupter: Codex stops the turn under way, as Esc
// does in its terminal.
func (s *CodexSource) Interrupt(ctx context.Context, sessionID string) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown codex session %q", sessionID)
	}
	thread := s.daemonThreadOf(session)
	server := s.connection()
	if thread == "" || server == nil {
		return errors.New("source: this Codex session can only be stopped from its terminal")
	}
	turn, err := s.activeTurn(ctx, server, thread)
	if err != nil {
		return err
	}
	if turn == "" {
		return nil // nothing is running
	}
	return server.call(ctx, "turn/interrupt", map[string]any{"threadId": thread, "turnId": turn}, nil)
}

// activeTurn is the id of the turn a thread is running, or "" when it is
// idle. The daemon lists the newest turn first.
func (s *CodexSource) activeTurn(ctx context.Context, server *codexAppServer, thread string) (string, error) {
	var turns struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := server.call(ctx, "thread/turns/list", map[string]any{"threadId": thread, "limit": 1}, &turns); err != nil {
		return "", err
	}
	if len(turns.Data) == 0 || turns.Data[0].Status != "inProgress" {
		return "", nil
	}
	return turns.Data[0].ID, nil
}

// connection is the connection to the daemon, nil when `am serve` has not
// made one.
func (s *CodexSource) connection() *codexAppServer {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	return s.appServer
}
