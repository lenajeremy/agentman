import type { Session } from "./protocol";

type AlertSession = Pick<Session, "id" | "state" | "question">;

export function sessionNeedsAnswer(session: AlertSession): boolean {
  return session.state === "waiting_input" || Boolean(session.question);
}

/**
 * Reconcile a full session snapshot with the set of sessions already announced.
 * A daemon snapshot arrives repeatedly, so merely checking for `question` would
 * schedule the same local notification on every poll.
 */
export function reconcileQuestionAlerts<T extends AlertSession>(
  announced: ReadonlySet<string>,
  sessions: readonly T[],
): { announced: Set<string>; newlyPending: T[] } {
  const next = new Set<string>();
  const newlyPending: T[] = [];
  for (const session of sessions) {
    if (!sessionNeedsAnswer(session)) continue;
    next.add(session.id);
    if (!announced.has(session.id)) newlyPending.push(session);
  }
  return { announced: next, newlyPending };
}

/** Update the alert state for one incremental session event. */
export function updateQuestionAlerts<T extends AlertSession>(
  announced: ReadonlySet<string>,
  session: T,
): { announced: Set<string>; newlyPending?: T } {
  const next = new Set(announced);
  if (!sessionNeedsAnswer(session)) {
    next.delete(session.id);
    return { announced: next };
  }
  if (next.has(session.id)) return { announced: next };
  next.add(session.id);
  return { announced: next, newlyPending: session };
}
