import { boardColumn, worktreeParts } from "./board.ts";
import type { Session } from "./protocol.ts";
import { folderName } from "./session-view.ts";
import type { Palette } from "./theme.ts";

/**
 * The colour each agent is shown in, so which is which can be told at a
 * glance, by the Mac's rules (desktop/Agentman/Design/AgentColors.swift). A
 * project or folder keeps its colour for good: each new one takes whichever
 * is used least so far, so few of them never share one.
 */
export type AgentColorBy = "none" | "state" | "project" | "folder";

export const AGENT_COLOR_CHOICES: { value: AgentColorBy; label: string }[] = [
  { value: "none", label: "None" },
  { value: "state", label: "State" },
  { value: "project", label: "Project" },
  { value: "folder", label: "Folder" },
];

export const parseAgentColorBy = (value: unknown): AgentColorBy =>
  value === "state" || value === "project" || value === "folder" ? value : "none";

/** The ten hues, for light and dark, as on the Mac. */
export const AGENT_HUES: { light: string; dark: string }[] = [
  { light: "#3268F5", dark: "#6A95FF" },
  { light: "#12A66A", dark: "#2BC784" },
  { light: "#E5484D", dark: "#FF6B70" },
  { light: "#8A3FB0", dark: "#C78BEA" },
  { light: "#D99A2B", dark: "#E7B04C" },
  { light: "#0E9F9A", dark: "#3CC9C2" },
  { light: "#D6409F", dark: "#F07BC4" },
  { light: "#5B6170", dark: "#A8AEBB" },
  { light: "#B25A12", dark: "#F2A867" },
  { light: "#4F46B8", dark: "#9A93F2" },
];

/** Worktrees of a repository are one project. */
export const projectName = (path: string): string => worktreeParts(path)?.repo ?? folderName(path);

/** What a project's or folder's colour is kept under; null when coloured by
 *  state or not at all. */
export function agentColorKey(cwd: string, by: AgentColorBy): string | null {
  if (by === "project") return `project|${projectName(cwd)}`;
  if (by === "folder") return `folder|${cwd}`;
  return null;
}

/**
 * Gives each key a colour the first time it is seen, the first of the least
 * used. Keeps what is assigned already; returns the same object when nothing
 * was new, so a caller can skip saving.
 */
export function assignHues(assigned: Readonly<Record<string, number>>, keys: readonly string[]): Record<string, number> {
  let next = assigned as Record<string, number>;
  for (const key of keys) {
    const known = next[key];
    if (known !== undefined && known >= 0 && known < AGENT_HUES.length) continue;
    const uses = AGENT_HUES.map(() => 0);
    for (const value of Object.values(next)) if (value >= 0 && value < uses.length) uses[value] += 1;
    if (next === assigned) next = { ...assigned };
    next[key] = uses.indexOf(Math.min(...uses));
  }
  return next;
}

/** The colour an agent is shown in, or null when none is chosen, or its
 *  project has not been given one yet. */
export function agentColor(
  session: Pick<Session, "state" | "question" | "cwd">,
  by: AgentColorBy,
  assigned: Readonly<Record<string, number>>,
  color: Pick<Palette, "needsYou" | "working" | "ok">,
  scheme: "light" | "dark",
): string | null {
  if (by === "none") return null;
  if (by === "state") {
    const column = boardColumn(session);
    return column === "needsYou" ? color.needsYou : column === "working" ? color.working : color.ok;
  }
  const index = assigned[agentColorKey(session.cwd, by)!];
  return index === undefined ? null : AGENT_HUES[index][scheme];
}

/** A colour laid over another at this strength, as an opaque "#rrggbb": a
 *  card's wash keeps its surface whatever is behind it. */
export function mixColors(base: string, over: string, amount: number): string {
  const channel = (color: string, at: number) => parseInt(color.slice(at, at + 2), 16);
  const part = (at: number) =>
    Math.round(channel(base, at) + (channel(over, at) - channel(base, at)) * amount)
      .toString(16)
      .padStart(2, "0");
  return `#${part(1)}${part(3)}${part(5)}`;
}

/** A coloured card's light wash and tinted border, on its surface. */
export const agentCardTint = (hue: string, surface: string) => ({
  backgroundColor: mixColors(surface, hue, 0.08),
  borderColor: mixColors(surface, hue, 0.45),
});
