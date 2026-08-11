import type { Session } from "./protocol";

/**
 * The rules for hiding a swiped-away session, kept free of any React Native
 * import so they can be tested with `node --test` and no test framework.
 *
 * Worth isolating: "hidden until it is triggered again" is a small rule with a
 * bad failure mode. Too eager and an agent waiting on you is invisible.
 */

/** What was true about a session when it was swiped away. */
interface Dismissal {
  /** The session's lastActivityAt at that moment. New activity is what
   *  distinguishes "still the thing I dismissed" from "it did something". */
  activityAt: number;
  /** When it was dismissed, so old entries can be pruned. */
  at: number;
}

export type Dismissals = Record<string, Dismissal>;

/**
 * Whether a session should be hidden right now.
 *
 * The rule is "hidden until it is triggered again", and there are exactly two
 * ways to be triggered: the agent starts doing something (any state that is not
 * idle), or it records new activity. Either one un-hides the row.
 *
 * Erring towards showing is deliberate. A session wrongly shown is clutter; a
 * session wrongly hidden is an agent waiting on you that you never find out
 * about.
 */
export function isHidden(session: Session, dismissals: Dismissals): boolean {
  const dismissal = dismissals[session.id];
  if (!dismissal) return false;
  if (session.state !== "idle") return false;
  return session.lastActivityAt <= dismissal.activityAt;
}

/** Records a dismissal, pinning it to the activity the user saw. */
export function dismiss(session: Session, dismissals: Dismissals): Dismissals {
  return {
    ...dismissals,
    [session.id]: { activityAt: session.lastActivityAt, at: Date.now() },
  };
}

/**
 * Drops dismissals that are no longer doing anything.
 *
 * Called only with a full, authoritative session list from a connected daemon.
 * Pruning against a partial list — or an empty one because the Mac is asleep —
 * would silently un-hide everything the next time it reconnected.
 *
 * Two things get dropped: sessions that have since been triggered again (the
 * entry has done its job), and sessions that no longer exist at all. The second
 * is what stops this growing without limit, and it is also correct: a session
 * id that comes back later is a session that was started again.
 */
export function prune(sessions: Session[], dismissals: Dismissals): Dismissals {
  const live = new Map(sessions.map((session) => [session.id, session]));
  const next: Dismissals = {};
  for (const [id, dismissal] of Object.entries(dismissals)) {
    const session = live.get(id);
    if (!session) continue;
    if (!isHidden(session, dismissals)) continue;
    next[id] = dismissal;
  }
  return next;
}

/** Whether swiping this row away is allowed. */
export function canDismiss(session: Session): boolean {
  // Only idle sessions. A busy one is working and would reappear immediately;
  // a waiting_input one is blocked on you specifically, and hiding that is how
  // an agent sits untouched for an hour.
  return session.state === "idle";
}
