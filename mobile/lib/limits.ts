import type { AgentLimits, LimitWindow } from "./protocol";

/**
 * The Limits tab's arithmetic and words, kept apart from the screen so they
 * can be tested: what the Mac app shows, in the same numbers and labels.
 */

export const LIMITS_FOOTNOTE =
  "Read from Claude Code and Codex on your Mac, as their own /usage and /status show. Other agents don't report plan limits.";

export type Pace = "ahead" | "on" | "under";

export const PACE_TITLE: Record<Pace, string> = {
  ahead: "Ahead of pace",
  on: "On pace",
  under: "Under pace",
};

/** The five hours, then the weeks, then the months, in the order the Mac
 *  sent them within each. */
export function orderedWindows(windows: readonly LimitWindow[]): LimitWindow[] {
  const rank = (kind: string) => ({ session: 0, weekly: 1, monthly: 2 } as Record<string, number>)[kind] ?? 3;
  return windows.map((window, index) => ({ window, index }))
    .sort((a, b) => rank(a.window.kind) - rank(b.window.kind) || a.index - b.index)
    .map(({ window }) => window);
}

/** The share left, a whole percent. */
export function leftPercent(window: Pick<LimitWindow, "used">): number {
  return Math.round(100 - Math.min(Math.max(window.used, 0), 100));
}

/** How far through its window the plan is, 0 to 1, when the window says how
 *  long it is and when it resets. */
export function elapsed(window: LimitWindow, now: number): number | null {
  if (!window.resetsAt || !window.minutes || window.minutes <= 0) return null;
  const length = window.minutes * 60_000;
  return Math.min(Math.max((length - (window.resetsAt - now)) / length, 0), 1);
}

/** What would be left by now, spending evenly through the window. */
export function evenLeft(window: LimitWindow, now: number): number | null {
  const through = elapsed(window, now);
  return through === null ? null : Math.round((1 - through) * 100);
}

/** Ahead when more than five points more is used than the window has passed,
 *  under when five fewer: T3 Code's rule. */
export function pace(window: LimitWindow, now: number): Pace | null {
  const through = elapsed(window, now);
  if (through === null) return null;
  const over = window.used - through * 100;
  return over > 5 ? "ahead" : over < -5 ? "under" : "on";
}

/** 4d 2h, 3h 12m, 12m. */
export function span(ms: number): string {
  const minutes = Math.floor(ms / 60_000);
  const days = Math.floor(minutes / 1440);
  const hours = Math.floor((minutes % 1440) / 60);
  const rest = minutes % 60;
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${rest}m`;
  return `${rest}m`;
}

/** "Resets in 3h 12m", or "Resets now" once it is past or all but. */
export function resetsLabel(window: LimitWindow, now: number): string | null {
  if (!window.resetsAt) return null;
  const left = window.resetsAt - now;
  return left < 60_000 ? "Resets now" : `Resets in ${span(left)}`;
}

/** When the numbers were read: "Checked 3m ago", or for Codex's records,
 *  "As of its last reply, 25m ago". */
export function checkedLabel(agent: Pick<AgentLimits, "checkedAt" | "lastTurn">, now: number): string {
  const ago = now - agent.checkedAt;
  const when = ago < 60_000 ? "just now" : `${span(ago)} ago`;
  return agent.lastTurn ? `As of its last reply, ${when}` : `Checked ${when}`;
}
