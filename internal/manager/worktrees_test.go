package manager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Agentman makes a worktree of an agent's repository, lists them, and
// removes one, asking the user first when there is work to lose.
func TestAgentmanMakesListsAndRemovesWorktrees(t *testing.T) {
	h, home := launchHarness(t)
	made := filepath.Join(home, "Agentman", "Worktrees", "site", "rate-limits")
	next := h.agents.answer
	h.agents.answer = func(req protocol.Request) protocol.Event {
		switch req.Type {
		case protocol.ReqCreateWorktree:
			return protocol.Event{Type: protocol.EvtWorktree, Worktree: &protocol.Worktree{
				Path: made, Name: "rate-limits", Repo: "site", Branch: "rate-limits", Agentman: true, Copied: []string{".env"},
			}}
		case protocol.ReqListWorktrees:
			return protocol.Event{Type: protocol.EvtWorktrees, Worktrees: []protocol.Worktree{
				{Path: filepath.Join(home, "code", "site"), Name: "site", Repo: "site", Branch: "main", Main: true},
				{Path: made, Name: "rate-limits", Repo: "site", Branch: "rate-limits", Agentman: true},
			}}
		case protocol.ReqRemoveWorktree:
			if !req.Force {
				return protocol.Event{Type: protocol.EvtError, Error: "workspace: it has uncommitted changes; commit them, or remove the worktree anyway to lose them"}
			}
			return protocol.Event{Type: protocol.EvtWorktrees}
		}
		return next(req)
	}

	result := h.m.runTool(context.Background(), "create_worktree", []byte(`{"folder":"site","name":"rate limits"}`))
	if result.isError || !strings.Contains(result.text, "on a new branch rate-limits") || !strings.Contains(result.text, "Copied in: .env") {
		t.Fatalf("create_worktree: %+v", result)
	}
	if sent := h.agents.sent(protocol.ReqCreateWorktree); len(sent) != 1 || sent[0].Path != "code/site" || sent[0].Text != "rate limits" {
		t.Errorf("create request = %+v", sent)
	}
	if result.activity != "Made the worktree rate-limits of site" {
		t.Errorf("activity = %q", result.activity)
	}

	listed := h.m.runTool(context.Background(), "list_worktrees", []byte(`{"folder":"~/code/site"}`))
	if listed.isError || !strings.Contains(listed.text, "the repository's own folder, on main") || !strings.Contains(listed.text, "rate-limits, on rate-limits (Agentman made it)") {
		t.Errorf("list_worktrees: %+v", listed)
	}

	// Work to lose: refused, with word to ask the user first.
	if err := os.MkdirAll(made, 0o755); err != nil {
		t.Fatal(err)
	}
	refused := h.m.runTool(context.Background(), "remove_worktree", []byte(`{"worktree":"`+made+`"}`))
	if !refused.isError || !strings.Contains(refused.text, "uncommitted changes") || !strings.Contains(refused.text, "Ask the user") {
		t.Errorf("remove_worktree without force: %+v", refused)
	}
	removed := h.m.runTool(context.Background(), "remove_worktree", []byte(`{"worktree":"`+made+`","force":true}`))
	if removed.isError || removed.activity != "Removed the worktree rate-limits" {
		t.Errorf("remove_worktree: %+v", removed)
	}
	if sent := h.agents.sent(protocol.ReqRemoveWorktree); len(sent) != 2 || sent[1].Path != "Agentman/Worktrees/site/rate-limits" || !sent[1].Force {
		t.Errorf("remove requests = %+v", sent)
	}
}
