import { type Folder } from "./protocol.ts";

/**
 * Folder filtering.
 *
 * Selecting a folder means the project, not one directory of it: a
 * repository's bin/ or mobile/ is the same work as its root, and a filter
 * that excluded them would not match the count that led you there. The Mac
 * decides this too, in internal/source/history.go; this is the same rule
 * applied to sessions that arrive while a folder is open.
 */

/** Whether a session's working directory is dir or sits beneath it. */
export function withinFolder(cwd: string, dir: string): boolean {
  if (!cwd || !dir) return false;
  const from = normalise(cwd);
  const to = normalise(dir);
  // The separator is what stops /code/api-old matching /code/api.
  return from === to || from.startsWith(to + "/");
}

/** Drop a trailing slash so "/code/api/" and "/code/api" are one folder. */
function normalise(path: string): string {
  const trimmed = path.replace(/\/+$/, "");
  return trimmed === "" ? "/" : trimmed;
}

/** The last two segments of a path, the way a folder reads on a phone. */
export function folderLabel(path: string): string {
  const parts = normalise(path).split("/").filter(Boolean);
  if (parts.length === 0) return "/";
  if (parts.length <= 2) return "/" + parts.join("/");
  return "~/" + parts.slice(-2).join("/");
}

/** One browsable child of the directory being viewed. */
export interface BrowsedFolder {
  name: string;
  /** Sessions recorded at or below it, absent from a Mac that predates them. */
  agents?: number;
  running?: number;
}

/**
 * Pair each browsable folder with its count.
 *
 * The listing and the counts arrive as two arrays because a count is only
 * sent for a folder that has agents under it — sending a zero for every
 * untouched folder in a home directory would be most of the payload.
 */
export function mergeCounts(names: string[], counts: Folder[]): BrowsedFolder[] {
  const byName = new Map(counts.map((folder) => [folder.path, folder]));
  return names.map((name) => {
    const folder = byName.get(name);
    return folder
      ? { name, agents: folder.agents, running: folder.running }
      : { name };
  });
}

/** "12 agents", "1 agent" — the count as a row reads it. */
export function agentCount(agents: number): string {
  return `${agents.toLocaleString()} ${agents === 1 ? "agent" : "agents"}`;
}
