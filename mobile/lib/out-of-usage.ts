/**
 * An agent out of usage, in words: when its plan comes back. Kept apart from
 * the screens so it can be tested without them.
 */

/** As much of a session's limit as the words need. */
export interface LimitLike {
  resetsAt?: number;
}

/** "7:19 PM" today, "Thu 7:19 PM" within the week, "Nov 30, 7:19 PM" after. */
export function backAt(resetsAt: number, now: number = Date.now()): string {
  const when = new Date(resetsAt);
  const today = new Date(now);
  const time = when.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  const day = (date: Date) => new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
  const days = Math.round((day(when) - day(today)) / 86_400_000);
  if (days <= 0) return time;
  if (days < 7) return `${when.toLocaleDateString([], { weekday: "short" })} ${time}`;
  return `${when.toLocaleDateString([], { month: "short", day: "numeric" })}, ${time}`;
}

/** "Out of usage until 7:19 PM", or "Out of usage" when the agent named no
 *  time. */
export function outOfUsageLabel(limit: LimitLike, now: number = Date.now()): string {
  return limit.resetsAt && limit.resetsAt > 0 ? `Out of usage until ${backAt(limit.resetsAt, now)}` : "Out of usage";
}

/** As much of a row as the rule below needs. */
export interface NoticeLike {
  id: string;
  notice?: string;
}

/** The usage-limit notice that still holds: the newest, while the agent is
 *  out of usage. Any other is history, and reads as a quiet line. */
export function liveLimitNotice(messages: readonly NoticeLike[], limit: LimitLike | undefined): string | undefined {
  if (!limit) return undefined;
  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i].notice === "limit") return messages[i].id;
  }
  return undefined;
}
