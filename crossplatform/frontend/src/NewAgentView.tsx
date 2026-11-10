// New Agent in the detail area (Views/NewAgentView.swift): the task box,
// the agent and folder menus, worktrees. To be built: PARITY.md §10.

export function NewAgentView({ worktree }: { worktree?: boolean }) {
  return <div className="empty big">{worktree ? "New worktree" : "What should we work on?"}</div>;
}
