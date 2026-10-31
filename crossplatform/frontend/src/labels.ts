// Words the Mac app shows, as it builds them (Views/Sidebar.swift,
// Views/AgentThreadView.swift, Design/SessionLabels.swift).

import type { Message, Session } from "@shared/protocol";

const WORKTREE = /\/Agentman\/Worktrees\/([^/]+)\/([^/]+)/;

/** The repository an agent works in: from ~/Agentman/Worktrees/<repo>/… for a worktree. */
export function repoName(cwd: string): string {
  const worktree = cwd.match(WORKTREE);
  if (worktree) return worktree[1];
  return cwd.replace(/\/+$/, "").split("/").pop() || cwd;
}

/** "repo · name" for a worktree, else the folder's name. */
export function folderLabel(cwd: string): string {
  const worktree = cwd.match(WORKTREE);
  if (worktree) return `${worktree[1]} · ${worktree[2]}`;
  return cwd.replace(/\/+$/, "").split("/").pop() || cwd;
}

/** The badge: the repository's first two letters. */
export function initials(cwd: string): string {
  return repoName(cwd).replace(/[^A-Za-z0-9]/g, "").slice(0, 2).toUpperCase() || "··";
}

/** "now", "4m", "3h", "2d". */
export function since(ms: number, now = Date.now()): string {
  const s = Math.max(0, (now - ms) / 1000);
  if (s < 60) return "now";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

export function needsYou(s: Session): boolean {
  return s.state === "waiting_input" || !!s.question;
}

const AGENT_NAMES: Record<string, string> = {
  claude: "Claude Code", codex: "Codex", cursor: "Cursor", "cursor-cli": "Cursor", opencode: "OpenCode",
  kiro: "Kiro", antigravity: "Antigravity", gemini: "Gemini", grok: "Grok",
};

export function agentName(kind: string): string {
  return AGENT_NAMES[kind] ?? kind;
}

// The Mac's words for a run of tool calls, as AgentThreadView counts them
// until Agentman's own line arrives. The phone's runCounts says a few of
// them differently ("Looked up a page"), so the Mac's are kept here.
const KINDS: [RegExp, string, string, string][] = [
  [/^(bash|shell|exec_command|run_shell_command|local_shell|terminal)$/i, "Ran", "command", "terminal"],
  [/^(read|read_file|view|cat|notebookread)$/i, "Read", "file", "read"],
  [/^(edit|multiedit|apply_patch|str_replace|str_replace_editor|notebookedit|replace)$/i, "Edited", "file", "edit"],
  [/^(write|create_file|write_file)$/i, "Wrote", "file", "edit"],
  [/^(grep|glob|search|find|list|ls|codebase_search|file_search)$/i, "Searched", "time", "search"],
  [/^(webfetch|fetch|websearch|web_search|web_fetch)$/i, "Fetched", "page", "web"],
  [/^(task|agent)$/i, "Delegated", "task", "task"],
  [/^(todowrite|update_plan)$/i, "Updated", "plan", "plan"],
];

export type ToolKind = "terminal" | "read" | "edit" | "search" | "web" | "task" | "plan" | "other";

export function toolKind(name: string): ToolKind {
  return (KINDS.find(([re]) => re.test(name))?.[3] as ToolKind) ?? "other";
}

export function runSummary(calls: readonly Message[]): string {
  const order: string[] = [];
  const counts = new Map<string, { verb: string; noun: string; n: number }>();
  for (const call of calls) {
    const kind = KINDS.find(([re]) => re.test(call.tool?.name ?? ""));
    const verb = kind?.[1] ?? "Used";
    const noun = kind?.[2] ?? "tool";
    const id = `${verb} ${noun}`;
    if (!counts.has(id)) order.push(id);
    const entry = counts.get(id) ?? { verb, noun, n: 0 };
    entry.n += 1;
    counts.set(id, entry);
  }
  const phrases = order.map((id, i) => {
    const { verb, noun, n } = counts.get(id)!;
    const v = i === 0 ? verb : verb.toLowerCase();
    if (verb === "Searched") return `${v} ${n === 1 ? "once" : `${n} times`}`;
    if (verb === "Updated") return `${v} the plan`;
    return `${v} ${n === 1 ? `a ${noun}` : `${n} ${noun}s`}`;
  });
  if (phrases.length <= 1) return phrases[0] ?? "Used a tool";
  return `${phrases.slice(0, -1).join(", ")} and ${phrases.at(-1)}`;
}
