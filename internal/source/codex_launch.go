package source

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Starting a Codex session from the phone or the Mac on the shared daemon.
//
// A launch used to open `codex -- <prompt>` in a pane and then find the pane's
// conversation by folder and timing, which cannot tell two Codex sessions in
// one folder apart, so a second launch in a folder was refused. Now the daemon
// starts the conversation and takes its first message, and the terminal is
// opened on it with `codex resume <id>` in a pane that names the thread (see
// ResumedSession), which discovery matches exactly.
//
// The conversation gets what a terminal launch in that folder gets: the
// daemon resolves the folder's trust and the user's config for it (checked on
// Codex 0.166: on-request with workspace-write in a trusted folder, read-only
// in one never trusted, as the terminal would have until asked).

// ErrNoCodexDaemon reports that the shared daemon cannot start the session,
// and a launch opens Codex in a pane as before.
var ErrNoCodexDaemon = errors.New("source: Codex's shared daemon is not available")

// codexLaunchHold is how long Agentman stays joined to a thread it started.
// Starting a thread joins the client that started it, and a joined thread
// stays open; once the terminal opened on it has joined too, Agentman lets go,
// so closing that terminal closes the session as it would any other.
var codexLaunchHold = 15 * time.Second

// codexRolloutWait bounds waiting for the first message's rollout, which
// `codex resume` needs.
const codexRolloutWait = 5 * time.Second

// StartThread starts a conversation in dir with its first message and
// returns its thread id.
func (s *CodexSource) StartThread(ctx context.Context, dir, prompt string) (string, error) {
	server := s.connection()
	streams := s.codexStreams()
	// A first message Codex's terminal would read itself (a slash command)
	// has to be typed there.
	if server == nil || streams == nil || !server.connected() || strings.TrimSpace(prompt) == "" || codexTerminalOnly(prompt) {
		return "", ErrNoCodexDaemon
	}
	var started struct {
		Thread codexWireThread `json:"thread"`
	}
	if err := server.call(ctx, "thread/start", map[string]any{"cwd": dir}, &started); err != nil {
		if codexFallBack(err) {
			return "", ErrNoCodexDaemon
		}
		return "", fmt.Errorf("source: Codex did not start the session: %w", err)
	}
	thread := started.Thread.thread()
	hold := streams.adopt(thread.ID)
	go func() {
		time.Sleep(codexLaunchHold)
		streams.close(hold)
	}()

	input := []any{map[string]any{"type": "text", "text": prompt, "text_elements": []any{}}}
	if err := server.call(ctx, "turn/start", map[string]any{"threadId": thread.ID, "input": input}, nil); err != nil {
		return "", fmt.Errorf("source: Codex started the session but did not take its first message: %w", err)
	}
	if thread.Path != "" {
		deadline := time.Now().Add(codexRolloutWait)
		for _, err := os.Stat(thread.Path); err != nil && time.Now().Before(deadline); _, err = os.Stat(thread.Path) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	return thread.ID, nil
}
