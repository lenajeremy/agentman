import type { Message } from "./protocol";

/** A generous phone-sized window; older history remains authoritative on Mac. */
export const MAX_RETAINED_MESSAGES = 1_000;

export interface RetainedMessages {
  messages: Message[];
  /** True when at least one otherwise valid message fell outside the window. */
  limited: boolean;
}

/**
 * A message Muse kept from before a compaction, under an id of its own, whose
 * original is already here: shown again, the chat would read twice. Muse cuts
 * what it keeps at 1,000 characters.
 */
function isKeptCopy(message: Message, existing: readonly Message[]): boolean {
  if (!message.id.startsWith("earlier:") || !message.text) return false;
  const text = message.text;
  return existing.some((held) => {
    if (held.id.startsWith("earlier:") || held.role !== message.role || !held.text) return false;
    const original = held.text.trim();
    return text.length >= 1000 ? original.startsWith(text) : original === text;
  });
}

/** What Muse kept and this phone never saw is older than anything held. */
function placedKeptCopies(incoming: readonly Message[], existing: readonly Message[]): readonly Message[] {
  const held = existing.filter((message) => !message.id.startsWith("earlier:")).map((message) => message.ts);
  if (held.length === 0) return incoming;
  const oldest = Math.min(...held);
  const lost = incoming.filter((message) => message.id.startsWith("earlier:") && !isKeptCopy(message, existing));
  let at = oldest - lost.length;
  return incoming.map((message) =>
    lost.includes(message) && message.ts >= oldest ? { ...message, ts: at++ } : message,
  );
}

/** Merge by stable id, order chronologically, and retain the newest window. */
export function mergeRetainedMessages(
  existing: readonly Message[],
  incoming: readonly Message[],
  limit = MAX_RETAINED_MESSAGES,
): RetainedMessages {
  if (limit <= 0) return { messages: [], limited: existing.length + incoming.length > 0 };
  const byId = new Map<string, Message>();
  for (const message of existing) byId.set(message.id, message);
  for (const message of placedKeptCopies(incoming, existing)) {
    if (isKeptCopy(message, existing)) continue;
    // A later copy replaces the earlier one, which is how a running tool call
    // arrives finished. Never the reverse: a history page read before the
    // result was written must not set a finished call running again.
    const known = byId.get(message.id);
    if (known?.tool && known.tool.status !== "running" && message.tool?.status === "running") continue;
    byId.set(message.id, message);
  }
  const ordered = Array.from(byId.values()).sort((a, b) => {
    if (a.ts !== b.ts) return a.ts - b.ts;
    return a.id < b.id ? -1 : 1;
  });
  const limited = ordered.length > limit;
  return {
    messages: limited ? ordered.slice(ordered.length - limit) : ordered,
    limited,
  };
}

/**
 * The cached sessions a full snapshot should drop: those no longer running
 * that no screen has open.
 *
 * A snapshot arrives on every reconnect and every return to the foreground,
 * and it lists only what is running. A past session opened from a folder is
 * never in it, so dropping everything absent emptied the transcript someone
 * was reading, with nothing to load it again.
 */
export function sessionsToForget(
  cached: Iterable<string>,
  live: ReadonlySet<string>,
  watched: ReadonlySet<string>,
): string[] {
  return Array.from(cached).filter((id) => !live.has(id) && !watched.has(id));
}

/**
 * Watched sessions that have just become reachable: newly running, or newly
 * in a place a message can be typed into.
 *
 * A resumed session keeps its id, so the screen showing it has no reason of
 * its own to subscribe again, and a subscription made while it was ended never
 * started a live tail on an older Mac.
 */
export function newlyReachable(
  previous: readonly { id: string; inject: string }[],
  next: readonly { id: string; inject: string }[],
  watched: ReadonlySet<string>,
): string[] {
  const before = new Map(previous.map((session) => [session.id, session.inject]));
  return next
    .filter((session) =>
      watched.has(session.id) &&
      session.inject !== "none" &&
      (before.get(session.id) ?? "none") === "none")
    .map((session) => session.id);
}
