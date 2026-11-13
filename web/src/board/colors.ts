// The colour each agent is shown in, so which is which can be told at a
// glance (Design/AgentColors.swift): by its project or its folder. Its state
// needs no colour of this kind: the dot beside it shows that. A project or
// folder keeps its colour for good: each new one takes whichever is used
// least so far, so few of them never share one.

import type { Session } from "@shared/protocol";
import { getPref, setPref, usePref } from "../prefs";

export type ColorBy = "none" | "project" | "folder";
export const COLOR_BY: { id: ColorBy; title: string }[] = [
  { id: "none", title: "No Color" }, { id: "project", title: "Project" }, { id: "folder", title: "Folder" },
];
const SETTING = "agentColorBy";
const ASSIGNED = "agentColorsAssigned";
const PALETTE = "agentColorsPalette";

/** None near a state's colour, so a folder never reads as one: blue is
 *  working, orange needs you, grey is idle. Light and dark, as CSS that
 *  follows the appearance. */
const HUES = [
  ["#12A66A", "#2BC784"], ["#8A3FB0", "#C78BEA"], ["#0E9F9A", "#3CC9C2"],
  ["#D6409F", "#F07BC4"], ["#6B9B1E", "#9BCB4A"], ["#9F1F4A", "#D4547A"],
].map(([light, dark]) => `light-dark(${light}, ${dark})`);

/** Where the colours kept from the first palette of ten went; its blue,
 *  indigo, slate, red, gold and orange were too near a state's. */
const MOVED: Record<number, number> = { 1: 0, 3: 1, 5: 2, 6: 3 };

export function useColorBy(): [ColorBy, (c: ColorBy) => void] {
  const [by, setBy] = usePref<string>(SETTING, "none");
  // A choice no longer offered (State) is no colour, as the Mac reads it.
  return [COLOR_BY.some((c) => c.id === by) ? (by as ColorBy) : "none", setBy];
}

export function agentColor(session: Session, by: ColorBy): string | null {
  switch (by) {
    case "none":
      return null;
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
  const assigned = assignments();
  const known = assigned[key];
  if (known !== undefined && known >= 0 && known < HUES.length) return known;
  const uses = HUES.map(() => 0);
  for (const v of Object.values(assigned)) if (v >= 0 && v < HUES.length) uses[v]++;
  const next = uses.indexOf(Math.min(...uses));
  setPref(ASSIGNED, { ...assigned, [key]: next });
  return next;
}

/** The colours given so far. Ones given from the first palette keep their
 *  colour where it is still here; the rest are given again. */
function assignments(): Record<string, number> {
  const assigned = getPref<Record<string, number>>(ASSIGNED, {});
  if (getPref<number>(PALETTE, 0) >= 2) return assigned;
  const kept: Record<string, number> = {};
  for (const [key, value] of Object.entries(assigned)) if (value in MOVED) kept[key] = MOVED[value];
  setPref(ASSIGNED, kept);
  setPref(PALETTE, 2);
  return kept;
}
