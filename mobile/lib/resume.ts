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
  state: string;
}): boolean {
  // Only an ended one. A session can also be unreachable because it is
  // running in someone's own terminal; reopening that by id started a second
  // agent beside the first, and the Mac now refuses it anyway.
  return session.inject === "none" && session.state === "ended" && canResume(session.kind);
}

/** How long a resume that answered stands in for the session it started. */
export const RESUME_GRACE_MS = 30_000;

/**
 * Wrap the request that reopens a session so the Mac is asked once.
 *
 * The pane answers within a second, but the agent in it takes a few more to
 * register, and until then the session still looks ended. Opening the screen
 * again in that window, or a second tap, asked again and started a second
 * process. Concurrent calls share one request; one that succeeded stands for
 * RESUME_GRACE_MS; one that failed may be tried again straight away.
 */
export function resumeOnce(
  start: (sessionId: string) => Promise<string>,
  now: () => number = Date.now,
): (sessionId: string) => Promise<string> {
  const started = new Map<string, { at: number; result: Promise<string> }>();
  return (sessionId) => {
    const previous = started.get(sessionId);
    if (previous && now() - previous.at < RESUME_GRACE_MS) return previous.result;
    const result = start(sessionId);
    started.set(sessionId, { at: now(), result });
    result.catch(() => {
      if (started.get(sessionId)?.result === result) started.delete(sessionId);
    });
    return result;
  };
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
