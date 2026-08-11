import type { Message } from "./protocol";

/**
 * Deciding when a catch-up fetch has caught up.
 *
 * Leaving a session screen unsubscribes the daemon's tail, so anything the
 * agent does while you are elsewhere is never pushed. Coming back subscribes
 * again — but that only streams what happens from that moment on, and since the
 * screen already has messages, nothing else would ever fetch the gap. It would
 * simply be missing, with no gap marker and no way to notice.
 *
 * So on re-entry the newest page is fetched and merged. One page usually
 * overlaps what was already there and the job is done; if the absence was
 * longer, the fetch walks further back until it connects.
 *
 * Kept here, free of React and React Native, because the stopping condition is
 * the part that can be quietly wrong in both directions: too eager and every
 * revisit drags the whole transcript down a phone connection, too lazy and the
 * feed has an invisible hole in it.
 */

/** How many messages a catch-up fetch asks for at a time. */
export const CATCH_UP_PAGE = 40;

/**
 * How far back a catch-up walks before giving up.
 *
 * Four pages covers any realistic "I switched apps for a minute". Past that the
 * rest stays reachable by scrolling up, which is the right trade: an unbounded
 * walk turns a day-long absence into a very expensive screen open.
 */
export const CATCH_UP_MAX_PAGES = 4;

export interface CatchUpState {
  sessionId: string;
  /** Timestamp of the newest message already on screen when we left. */
  sinceTs: number;
  /** Pages fetched so far in this catch-up. */
  depth: number;
}

/**
 * Whether another page is needed to close the gap.
 *
 * Messages in a page are chronological, so the first is the oldest. If even
 * that one is newer than what we already had, the two ranges do not touch yet
 * and there is still history in between.
 */
export function needsAnotherPage(
  pageMessages: Message[],
  hasMore: boolean,
  state: CatchUpState,
): boolean {
  if (pageMessages.length === 0) return false;
  if (!hasMore) return false;
  if (state.depth + 1 >= CATCH_UP_MAX_PAGES) return false;
  return pageMessages[0].ts > state.sinceTs;
}

/** The newest timestamp on screen, which is where a catch-up starts from. */
export function newestTimestamp(messages: Message[]): number {
  let newest = 0;
  for (const message of messages) {
    if (message.ts > newest) newest = message.ts;
  }
  return newest;
}
