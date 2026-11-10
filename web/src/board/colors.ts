// The colour each agent is shown in, so which is which can be told at a
// glance (Design/AgentColors.swift): by its state, its project or its folder.
// A project or folder keeps its colour for good: each new one takes
// whichever is used least so far, so few of them never share one.

import type { Session } from "@shared/protocol";
import { getPref, setPref, usePref } from "../prefs";
import { filterState } from "../shell/sections";

export type ColorBy = "none" | "state" | "project" | "folder";
export const COLOR_BY: { id: ColorBy; title: string }[] = [
  { id: "none", title: "No Color" }, { id: "state", title: "State" }, { id: "project", title: "Project" }, { id: "folder", title: "Folder" },
];
const SETTING = "agentColorBy";
const ASSIGNED = "agentColorsAssigned";

/** The ten hues, light and dark, as CSS that follows the appearance. */
const HUES = [
  ["#3268F5", "#6A95FF"], ["#12A66A", "#2BC784"], ["#E5484D", "#FF6B70"], ["#8A3FB0", "#C78BEA"], ["#D99A2B", "#E7B04C"],
  ["#0E9F9A", "#3CC9C2"], ["#D6409F", "#F07BC4"], ["#5B6170", "#A8AEBB"], ["#B25A12", "#F2A867"], ["#4F46B8", "#9A93F2"],
].map(([light, dark]) => `light-dark(${light}, ${dark})`);

export function useColorBy(): [ColorBy, (c: ColorBy) => void] {
  return usePref<ColorBy>(SETTING, "none");
}

export function agentColor(session: Session, by: ColorBy): string | null {
  switch (by) {
    case "none":
      return null;
    case "state": {
      const state = filterState(session);
      return state === "waiting_input" ? "var(--needs-you)" : state === "busy" ? "var(--working)" : "var(--ok)";
    }
    case "project":
      return HUES[index("project|" + projectName(session.cwd))];
    case "folder":
      return HUES[index("folder|" + session.cwd)];
  }
}

/** Worktrees of a repository are one project. */
function projectName(path: string): string {
  const worktree = /\/Agentman\/Worktrees\/([^/]+)\//.exec(path + "/");
  return worktree?.[1] ?? (path.replace(/\/+$/, "").split("/").pop() || path);
}

/** The colour a key has, giving it one the first time. */
function index(key: string): number {
  const assigned = getPref<Record<string, number>>(ASSIGNED, {});
  const known = assigned[key];
  if (known !== undefined && known >= 0 && known < HUES.length) return known;
  const uses = HUES.map(() => 0);
  for (const v of Object.values(assigned)) if (v >= 0 && v < HUES.length) uses[v]++;
  const next = uses.indexOf(Math.min(...uses));
  setPref(ASSIGNED, { ...assigned, [key]: next });
  return next;
}
