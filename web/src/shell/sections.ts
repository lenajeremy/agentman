// The sidebar's agents in sections (Stores/SidebarSections.swift): the ones
// pinned on top, then those still working, then the active ones (those that
// came back to you, finished or asking something), then the archived: work
// the user is done with, out of the way but not gone. And the user's choices
// behind them, kept across launches (AgentsStore.swift's sidebarChoices).

import { useSyncExternalStore } from "react";
import type { Session } from "@shared/protocol";
import { getPref, setPref } from "../prefs";
import type { FilterState } from "./state";

/** An agent idle this long is archived by itself. */
export const ARCHIVE_AFTER = 3 * 24 * 60 * 60 * 1000;

/** What the user decided: the agents pinned, in their order; those archived
 *  by hand, and when; those brought back by hand, and when (ms). */
export interface Choices {
  pinned: string[];
  archived: Record<string, number>;
  unarchived: Record<string, number>;
}

export interface Sections {
  pinned: Session[];
  /** What waits on the user, before what is busy: it never sinks below a
   *  long Working list. */
  needsYou: Session[];
  working: Session[];
  active: Session[];
  archived: Session[];
}

/** A session's status as the filters count it: waiting on the user whenever
 *  a question is up. */
export function filterState(s: Session): FilterState {
  if (s.question || s.state === "waiting_input") return "waiting_input";
  return s.state === "busy" ? "busy" : "idle";
}

export function inFolder(cwd: string, folder: string | null): boolean {
  if (!folder) return true;
  return cwd === folder || cwd.startsWith(folder.endsWith("/") ? folder : folder + "/");
}

/** Archived: by hand, until the agent works or asks again; or by itself once
 *  it has ended or sat idle for three days, unless the user brought it back
 *  since it last did anything. An agent working or waiting never is. */
export function isArchived(s: Session, choices: Choices, now = Date.now()): boolean {
  const state = filterState(s);
  if (state === "busy" || state === "waiting_input") return false;
  const active = s.lastActivityAt || 0;
  const archivedAt = choices.archived[s.id];
  if (archivedAt !== undefined) return active <= archivedAt;
  const unarchivedAt = choices.unarchived[s.id];
  if (unarchivedAt !== undefined && active <= unarchivedAt) return false;
  if (s.state === "ended") return true;
  return now - active > ARCHIVE_AFTER;
}

export function makeSections(sessions: Session[], choices: Choices, now = Date.now()): Sections {
  const sections: Sections = { pinned: [], needsYou: [], working: [], active: [], archived: [] };
  const pinned = new Map<string, Session>();
  for (const s of sessions) {
    if (isArchived(s, choices, now)) sections.archived.push(s);
    else if (choices.pinned.includes(s.id)) pinned.set(s.id, s);
    else if (filterState(s) === "waiting_input") sections.needsYou.push(s);
    else if (filterState(s) === "busy") sections.working.push(s);
    else sections.active.push(s);
  }
  sections.pinned = choices.pinned.map((id) => pinned.get(id)).filter((s): s is Session => !!s);
  // Whatever came back to you last first.
  const recent = (a: Session, b: Session) => (b.lastActivityAt || 0) - (a.lastActivityAt || 0);
  sections.needsYou.sort(recent);
  sections.active.sort(recent);
  sections.working.sort(recent);
  sections.archived.sort(recent);
  return sections;
}

/** Every agent shown, in the order the sidebar lists them, folded sections
 *  left out: what the arrow keys step through. */
export function shownInOrder(s: Sections, workingOpen: boolean, archivedOpen: boolean): Session[] {
  return [...s.pinned, ...s.needsYou, ...(workingOpen ? s.working : []), ...s.active, ...(archivedOpen ? s.archived : [])];
}

// The choices, kept as the Mac keeps them: ids, and dates in seconds.
const KEY = "sidebarChoices";
interface Saved {
  pinned?: string[];
  archived?: Record<string, number>;
  unarchived?: Record<string, number>;
}

function load(): Choices {
  const saved = getPref<Saved>(KEY, {});
  const ms = (dates?: Record<string, number>) => Object.fromEntries(Object.entries(dates ?? {}).map(([k, v]) => [k, v * 1000]));
  return { pinned: saved.pinned ?? [], archived: ms(saved.archived), unarchived: ms(saved.unarchived) };
}

let choices = load();
const listeners = new Set<() => void>();

function save(next: Choices, live: Set<string>) {
  choices = next;
  // A bounded few: choices for agents long gone are let go.
  const trim = (dates: Record<string, number>) => {
    const entries = Object.entries(dates);
    const kept = entries.length > 200 ? entries.filter(([k]) => live.has(k)) : entries;
    return Object.fromEntries(kept.map(([k, v]) => [k, v / 1000]));
  };
  setPref<Saved>(KEY, { pinned: next.pinned, archived: trim(next.archived), unarchived: trim(next.unarchived) });
  for (const l of listeners) l();
}

export function useChoices(): Choices {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    () => choices,
  );
}

export function getChoices(): Choices {
  return choices;
}

function without(dates: Record<string, number>, id: string) {
  const { [id]: _, ...rest } = dates;
  void _;
  return rest;
}

export function pin(id: string, live: Set<string>) {
  // A pinned agent is live work again.
  save({
    pinned: [...choices.pinned.filter((p) => p !== id), id],
    archived: without(choices.archived, id),
    unarchived: { ...choices.unarchived, [id]: Date.now() },
  }, live);
}

export function unpin(id: string, live: Set<string>) {
  save({ ...choices, pinned: choices.pinned.filter((p) => p !== id) }, live);
}

/** Done with: out of the active list, its pin with it. It comes back if the
 *  agent works or asks again. */
export function archive(id: string, live: Set<string>) {
  save({
    pinned: choices.pinned.filter((p) => p !== id),
    unarchived: without(choices.unarchived, id),
    archived: { ...choices.archived, [id]: Date.now() },
  }, live);
}

/** Back to active work, and kept there until it next does something. */
export function unarchive(id: string, live: Set<string>) {
  save({ ...choices, archived: without(choices.archived, id), unarchived: { ...choices.unarchived, [id]: Date.now() } }, live);
}

// Another window changed them.
window.addEventListener("storage", (e) => {
  if (e.key !== `agentman.${KEY}`) return;
  choices = load();
  for (const l of listeners) l();
});
