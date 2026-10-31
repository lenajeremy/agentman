package manager

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Worktrees, so the user can have several agents on one repository at once,
// each on a branch of its own: Agentman makes one, starts agents in it with
// start_agent, and removes it when asked.

func runCreateWorktree(ctx context.Context, m *Manager, input map[string]any) toolResult {
	where, _ := input["folder"].(string)
	_, rel, err := m.launchFolder(where)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	name, _ := input["name"].(string)
	if strings.TrimSpace(name) == "" {
		return toolResult{text: "Name the worktree: a few words for the work it's for, such as rate-limits.", isError: true}
	}
	event := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqCreateWorktree, Path: rel, Text: name})
	if event.Type == protocol.EvtError || event.Worktree == nil {
		return toolResult{text: "Couldn't make it: " + plainError(event.Error), isError: true}
	}
	worktree := event.Worktree
	text := fmt.Sprintf("Made the worktree %s of %s at %s, on a new branch %s from what %s had checked out.",
		worktree.Name, worktree.Repo, shortPath(worktree.Path), worktree.Branch, shortPath(filepath.Join(homeDir(), rel)))
	if len(worktree.Copied) > 0 {
		text += " Copied in: " + strings.Join(worktree.Copied, ", ") + "."
	}
	text += " Start agents there with start_agent, folder " + shortPath(worktree.Path) + "."
	return toolResult{text: text, activity: fmt.Sprintf("Made the worktree %s of %s", worktree.Name, worktree.Repo)}
}

func runListWorktrees(ctx context.Context, m *Manager, input map[string]any) toolResult {
	where, _ := input["folder"].(string)
	_, rel, err := m.launchFolder(where)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	event := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqListWorktrees, Path: rel})
	if event.Type == protocol.EvtError {
		return toolResult{text: "Couldn't list them: " + plainError(event.Error), isError: true}
	}
	var b strings.Builder
	for _, worktree := range event.Worktrees {
		branch := worktree.Branch
		if branch == "" {
			branch = "(detached)"
		}
		switch {
		case worktree.Main:
			fmt.Fprintf(&b, "- %s: the repository's own folder, on %s\n", shortPath(worktree.Path), branch)
		case worktree.Agentman:
			fmt.Fprintf(&b, "- %s: %s, on %s (Agentman made it)\n", shortPath(worktree.Path), worktree.Name, branch)
		default:
			fmt.Fprintf(&b, "- %s: %s, on %s (not Agentman's)\n", shortPath(worktree.Path), worktree.Name, branch)
		}
	}
	if b.Len() == 0 {
		return toolResult{text: "No worktrees."}
	}
	return toolResult{text: b.String(), activity: "Listed the worktrees of " + filepath.Base(rel)}
}

func runRemoveWorktree(ctx context.Context, m *Manager, input map[string]any) toolResult {
	where, _ := input["worktree"].(string)
	_, rel, err := m.launchFolder(where)
	if err != nil {
		return toolResult{text: err.Error(), isError: true}
	}
	force, _ := input["force"].(bool)
	event := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqRemoveWorktree, Path: rel, Force: force})
	if event.Type == protocol.EvtError {
		problem := plainError(event.Error)
		if !force && (strings.Contains(problem, "uncommitted") || strings.Contains(problem, "still working")) {
			problem += ". Ask the user before removing it with force"
		}
		return toolResult{text: "Didn't remove it: " + problem + ".", isError: true}
	}
	name := filepath.Base(rel)
	return toolResult{
		text:     fmt.Sprintf("Removed the worktree %s; its branch stays, with any commits on it.", name),
		activity: "Removed the worktree " + name,
	}
}
