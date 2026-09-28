/**
 * Which sessions can be reopened, and which are read-only for good.
 *
 * Mirrors ResumeArgs in internal/source/resume.go. The Mac is the authority —
 * it is the one that knows whether the CLI is installed and what the flag is
 * called — but the app has to decide whether to offer the thing at all, and
 * offering it where it can never work is worse than not offering it.
 */

/** Agents that can reopen one of their own sessions by id. */
const RESUMABLE = new Set([
  "claude",
  "codex",
  "cursor-cli",
  "kiro",
  "antigravity",
]);

export function canResume(kind: string): boolean {
  return RESUMABLE.has(kind);
}

/**
 * Whether opening this session should reopen it on the Mac.
 *
 * Only when there is no way to reach it otherwise. A session already running
 * in a pane takes messages as it is, and OpenCode takes them over its API
 * whenever its server is up — reopening either would start a second process
 * for a conversation that already has one.
 */
export function shouldResume(session: {
  kind: string;
  inject: string;
}): boolean {
  return session.inject === "none" && canResume(session.kind);
}

/**
 * Whether the pane behind this session is ours to close.
 *
 * Only a pane Agentman started. A session someone is running in their own
 * terminal reaches the phone through the hook queue, and killing that window
 * out from under them is not what "end session" means here.
 */
export function canEnd(session: { kind: string; inject: string }): boolean {
  return session.inject === "tmux" && canResume(session.kind);
}
