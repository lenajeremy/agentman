// The folders the sidebar's filter offers (Stores/FolderChoices.swift): the
// ones agents worked in and the ones the user chose, newest first, without
// the places apps keep for themselves or folders that are gone; the one
// filtered to among them.

import type { Folder } from "@shared/protocol";

/** How many are offered. */
export const SHOWN = 8;
/** How many folders the user chose are remembered. */
export const KEPT = 10;
export const CHOSEN_KEY = "chosenFolders";

/** Chosen folders, with when (seconds, as the Mac keeps them). */
export type Chosen = Record<string, number>;

/** The candidates, newest first, before any is checked to exist. */
export function candidates(folders: readonly Folder[], chosen: Chosen, home: string): string[] {
  const latest = new Map<string, number>();
  for (const folder of folders) latest.set(folder.path, (folder.lastActivityAt ?? 0) / 1000);
  for (const [path, at] of Object.entries(chosen)) latest.set(path, Math.max(latest.get(path) ?? -Infinity, at));
  return [...latest]
    .filter(([path]) => !isAppPlace(path, home))
    .sort(([a, at], [b, bt]) => bt - at || (b > a ? 1 : b < a ? -1 : 0))
    .map(([path]) => path);
}

/** The ones shown: the first few that exist, and the one filtered to. */
export function shown(paths: readonly string[], exists: (path: string) => boolean, selected: string | null): string[] {
  const out = paths.filter(exists).slice(0, SHOWN);
  if (selected && !out.includes(selected)) out.unshift(selected);
  return out;
}

/** A place an app keeps for itself rather than one the user works in. */
export function isAppPlace(path: string, home: string): boolean {
  return ["/tmp/", "/private/tmp/", "/var/folders/", "/private/var/folders/", ...(home ? [home + "/Library/"] : [])].some((p) => path.startsWith(p));
}

/** Where a folder is: the folder holding it, from home. */
export function place(path: string, home: string): string {
  const parent = path.replace(/\/+$/, "").replace(/\/[^/]*$/, "") || "/";
  if (home && parent === home) return "~";
  if (home && parent.startsWith(home + "/")) return "~" + parent.slice(home.length);
  return parent;
}

/** A folder the user chose, kept among the newest few. */
export function remember(chosen: Chosen, path: string, now = Date.now() / 1000): Chosen {
  const all = Object.entries({ ...chosen, [path]: now }).sort(([, a], [, b]) => b - a).slice(0, KEPT);
  return Object.fromEntries(all);
}
