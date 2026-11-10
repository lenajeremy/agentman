// Which notifications the user wants (App/NotificationPolicy.swift): a
// switch per kind, muted folders and their subfolders, quiet hours that may
// run past midnight (needs-you may still come through), and whether an agent
// speaks for itself while Agentman speaks for the agents. Settings ▸
// Notifications sets the same keys; the Agentman area asks it before
// posting Agentman's own.

import { getPref, setPref } from "../prefs";

export type NotificationKind = "finished" | "needsYou" | "agentman" | "noteSuggestion";

export const Key = {
  finished: "notifyFinished",
  needsYou: "notifyNeedsYou",
  agentman: "notifyAgentman",
  notes: "notifyNotes",
  sound: "notifySound",
  mutedFolders: "mutedFolders",
  quiet: "quietHours",
  quietStart: "quietStart",
  quietEnd: "quietEnd",
  quietLetsNeedsYou: "quietLetsNeedsYou",
  /** Each agent's own notifications while Agentman is on. Off unless switched on. */
  everyAgent: "notifyEveryAgent",
} as const;

/** Minutes after midnight quiet hours start and end, by default. */
export const DEFAULT_QUIET_START = 22 * 60;
export const DEFAULT_QUIET_END = 8 * 60;

const KIND_KEY: Record<NotificationKind, string> = {
  finished: Key.finished,
  needsYou: Key.needsYou,
  agentman: Key.agentman,
  noteSuggestion: Key.notes,
};

/** A setting that is on until the user switches it off. */
function flag(key: string): boolean {
  return getPref<boolean>(key, true) !== false;
}

/** Whether to tell the user about something of kind, from an agent working in folder. */
export function shouldNotify(kind: NotificationKind, folder?: string, now = new Date()): boolean {
  if (!flag(KIND_KEY[kind])) return false;
  if (folder && isMuted(folder)) return false;
  if (inQuietHours(now)) return kind === "needsYou" && flag(Key.quietLetsNeedsYou);
  return true;
}

/** Whether an agent notifies for itself. While Agentman is on it hears every
 *  agent and tells the user what matters, so it is the one voice: an agent
 *  speaks only when the user asked to hear from it, or from every agent. */
export function agentSpeaks(agentmanOn: boolean, asked: boolean): boolean {
  return !agentmanOn || asked || getPref<boolean>(Key.everyAgent, false) === true;
}

export function sound(): boolean {
  return flag(Key.sound);
}

export function mutedFolders(): string[] {
  return getPref<string[]>(Key.mutedFolders, []);
}

/** A path with "." and ".." resolved and no trailing slash, as standardizingPath. */
function standard(path: string): string {
  const parts: string[] = [];
  for (const part of path.split("/")) {
    if (!part || part === ".") continue;
    if (part === "..") parts.pop();
    else parts.push(part);
  }
  return (path.startsWith("/") ? "/" : "") + parts.join("/");
}

/** A folder is muted when it, or a folder it is in, is on the list. */
export function isMuted(folder: string): boolean {
  const f = standard(folder);
  return mutedFolders().some((m) => {
    const muted = standard(m);
    return f === muted || f.startsWith(muted + "/");
  });
}

export function setMuted(folder: string, muted: boolean) {
  const folders = mutedFolders().filter((f) => f !== folder);
  if (muted) folders.push(folder);
  setPref(Key.mutedFolders, folders.sort());
}

/** Whether now falls in quiet hours, which may run past midnight. */
export function inQuietHours(now = new Date()): boolean {
  if (getPref<boolean>(Key.quiet, false) !== true) return false;
  const start = getPref<number>(Key.quietStart, DEFAULT_QUIET_START);
  const end = getPref<number>(Key.quietEnd, DEFAULT_QUIET_END);
  const minute = now.getHours() * 60 + now.getMinutes();
  if (start === end) return false;
  return start < end ? minute >= start && minute < end : minute >= start || minute < end;
}
