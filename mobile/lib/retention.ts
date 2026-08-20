import type { Message } from "./protocol";

/** A generous phone-sized window; older history remains authoritative on Mac. */
export const MAX_RETAINED_MESSAGES = 1_000;

export interface RetainedMessages {
  messages: Message[];
  /** True when at least one otherwise valid message fell outside the window. */
  limited: boolean;
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
  for (const message of incoming) byId.set(message.id, message);
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
