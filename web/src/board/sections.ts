// The Board's columns (Stores/Board.swift's BoardSections): the agents
// sorted by what they want from the user, answering, waiting, or nothing.
// Agents move between them by themselves, as their states change. Pure, so
// its tests run under Node (sections.test.mts).

import type { ManagerEntry, Session } from "@shared/protocol";

export interface BoardSections {
  needsYou: Session[];
  working: Session[];
  idle: Session[];
  /** Put away by the user, and left out of the columns. */
  hidden: Session[];
}

export interface BoardInputs {
  /** A session's status as the filters count it (shell/sections.ts). */
  stateOf: (s: Session) => "waiting_input" | "busy" | "idle";
  /** Whether the sidebar has it archived. */
  archived: (s: Session) => boolean;
  /** Those taken off the Board by hand. */
  hidden: Set<string>;
  /** Those Agentman has put a card up about, which need the user though
   *  they sit idle. */
  waiting: Set<string>;
  /** Only those in this folder, with its subfolders. */
  folder?: string | null;
  /** A search's matches: only those, archived and hidden ones too. */
  matching?: Set<string> | null;
}

/** Sorts the agents. One that is working is working, card or not: it is not
 *  waiting on anyone now, and if it still needs the user when it stops, its
 *  card brings it back. Needing the user beats being hidden or archived; an
 *  agent that has ended, or that the sidebar has archived, isn't on the
 *  Board at all. */
export function makeBoard(sessions: Session[], inputs: BoardInputs): BoardSections {
  const sections: BoardSections = { needsYou: [], working: [], idle: [], hidden: [] };
  const { folder, matching } = inputs;
  for (const s of sessions) {
    if (s.state === "ended" || !inFolder(s.cwd, folder ?? null) || (matching && !matching.has(s.id))) continue;
    const state = inputs.stateOf(s);
    if (state === "waiting_input" || (inputs.waiting.has(s.id) && state !== "busy")) sections.needsYou.push(s);
    else if (!matching && inputs.archived(s)) continue;
    else if (!matching && inputs.hidden.has(s.id)) sections.hidden.push(s);
    else if (state === "busy") sections.working.push(s);
    else sections.idle.push(s);
  }
  const recent = (a: Session, b: Session) => order(b.lastActivityAt ?? 0, a.lastActivityAt ?? 0, b.id, a.id);
  // A working agent's last activity moves every second: by when it
  // started, its card stays put.
  const started = (a: Session, b: Session) => order(b.startedAt ?? 0, a.startedAt ?? 0, b.id, a.id);
  sections.needsYou.sort(recent);
  sections.working.sort(started);
  sections.idle.sort(recent);
  sections.hidden.sort(recent);
  return sections;
}

function order(x: number, y: number, xid: string, yid: string): number {
  return x !== y ? x - y : xid < yid ? -1 : xid > yid ? 1 : 0;
}

function inFolder(cwd: string, folder: string | null): boolean {
  if (!folder) return true;
  return cwd === folder || cwd.startsWith(folder.endsWith("/") ? folder : folder + "/");
}

export function shown(s: BoardSections): number {
  return s.needsYou.length + s.working.length + s.idle.length;
}

/** The line across the top of the Board. */
export function headline(s: BoardSections, connected: boolean): string {
  if (!connected) return "Waiting to reach your agents.";
  const others = s.working.length + s.idle.length;
  if (s.needsYou.length) {
    const waiting = s.needsYou.length === 1 ? "1 agent is waiting on you." : `${s.needsYou.length} agents are waiting on you.`;
    return others > 0 ? `${waiting} The rest are fine.` : waiting;
  }
  if (s.working.length) {
    return "Nothing needs you. " + (s.working.length === 1 ? "1 agent is working." : `${s.working.length} agents are working.`);
  }
  return s.idle.length === 0 && s.hidden.length === 0 ? "No agents running." : "All quiet. Nothing needs you.";
}

/** The line under it: who is working, by name, and how many are idle. */
export function detail(s: BoardSections): string {
  const parts: string[] = [];
  if (s.working.length) {
    const names = s.working.slice(0, 2).map((w) => w.name);
    const more = s.working.length - names.length;
    const who = more > 0 ? `${names.join(", ")} and ${more} more` : names.join(" and ");
    parts.push(who + (s.working.length === 1 ? " is working" : " are working"));
  }
  if (s.idle.length && (s.working.length || s.needsYou.length)) {
    parts.push(s.idle.length === 1 ? "1 is idle" : `${s.idle.length} are idle`);
  }
  return parts.length ? parts.join(", ") + "." : "";
}

/** The agents Agentman's latest answer is about: those it looked into and
 *  those it names. */
export function mentioned(entries: ManagerEntry[], sessions: Session[]): Set<string> {
  let start = 0;
  for (let i = entries.length - 1; i >= 0; i--) {
    if (entries[i].role === "user") {
      start = i + 1;
      break;
    }
  }
  const turn = entries.slice(start);
  const ids = new Set<string>();
  for (const e of turn) if (e.role === "activity" && e.sessionId) ids.add(e.sessionId);
  const said = turn.filter((e) => e.role === "assistant").map((e) => e.text ?? "").join("\n").toLowerCase();
  for (const s of sessions) if (s.name.length >= 4 && said.includes(s.name.toLowerCase())) ids.add(s.id);
  return ids;
}
