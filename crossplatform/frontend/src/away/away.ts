// What happened while the user was away (Stores/AwayLog.swift): away is the
// app in the background, the screen locked or the computer asleep. On
// their return, when it was long enough and something happened, a summary
// of each agent that did something (AwayDigest); Go ▸ What Happened While I
// Was Away shows the last one again, or the past hour.

import { useSyncExternalStore } from "react";
import type { Brief, ManagerEntry, Session } from "@shared/protocol";
import { daemon } from "../daemon";
import { getPref } from "../prefs";
import { appActive, onActive, onAway } from "../shell/native";
import { ask } from "../shell/state";
import { getStore } from "../store";

export interface AwayEvent {
  sessionId: string;
  kind: "finished" | "needsYou";
  text?: string;
  at: number;
}

export interface AwayAgent {
  id: string;
  name: string;
  kind: string;
  folder: string;
  /** Turns it finished while the user was away. */
  finished: number;
  /** Requests Agentman approved for it meanwhile. */
  approved: number;
  /** What it did: Agentman's brief when fresh, or what it said last. */
  summary?: string;
  /** What it waits on the user for, now. */
  waiting?: string;
}

export interface AwayDigest {
  id: number;
  since: number;
  until: number;
  agents: AwayAgent[];
}

const DAY = 24 * 3600 * 1000;
let events: AwayEvent[] = [];
let leftAt: number | null = null;
let digest: AwayDigest | null = null;
let last: AwayDigest | null = null;
let nextId = 1;
const listeners = new Set<() => void>();

function changed() {
  for (const l of listeners) l();
}

/** Keeps an event: a day of them at most, and never more than 3000. */
export function record(kind: AwayEvent["kind"], sessionId: string, text?: string, at = Date.now()) {
  events.push({ sessionId, kind, text, at });
  const cutoff = at - DAY;
  if (events.length > 3000 || (events[0] && events[0].at < cutoff)) {
    events = events.filter((e) => e.at >= cutoff);
    if (events.length > 3000) events = events.slice(events.length - 3000);
  }
}

export function left(at = Date.now()) {
  if (leftAt === null) leftAt = at;
}

/** A reply's first sentence or two, on one line. */
export function oneLine(text: string): string | undefined {
  const flat = text
    .replace(/[#*_`>]/g, "")
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter(Boolean)
    .join(" ");
  if (!flat) return undefined;
  const chars = [...flat];
  return chars.length > 180 ? chars.slice(0, 177).join("") + "…" : flat;
}

export function makeDigest(since: number, until: number, log: AwayEvent[], sessions: Session[], briefs: Record<string, Brief>, entries: ManagerEntry[]): AwayDigest {
  const window = log.filter((e) => e.at >= since && e.at <= until);
  const approvals = entries.filter((e) => e.role === "activity" && e.tool === "approve" && e.ts >= since);
  const agents: AwayAgent[] = [];
  for (const s of sessions) {
    const own = window.filter((e) => e.sessionId === s.id);
    const approved = approvals.filter((e) => e.sessionId === s.id).length;
    const active = (s.lastActivityAt || 0) >= since;
    const waitingSince = own.some((e) => e.kind === "needsYou");
    const waiting = s.state === "waiting_input" && (active || waitingSince) ? (s.question?.title || "Waiting on you") : undefined;
    if (!active && !own.length && !approved) continue;
    const finished = own.filter((e) => e.kind === "finished").length;
    let summary: string | undefined;
    const brief = briefs[s.id];
    if (brief && brief.updatedAt >= since && brief.summary) {
      summary = brief.summary;
    } else {
      const said = [...own].reverse().find((e) => e.kind === "finished" && e.text);
      if (said?.text) summary = oneLine(said.text);
    }
    agents.push({ id: s.id, name: s.name, kind: s.kind, folder: s.cwd, finished, approved, summary, waiting });
  }
  // Waiting on the user first, then the busiest.
  agents.sort((a, b) => {
    if (!!a.waiting !== !!b.waiting) return a.waiting ? -1 : 1;
    return b.finished + b.approved - (a.finished + a.approved);
  });
  return { id: nextId++, since, until, agents };
}

/** Agentman's briefs and its recent conversation, for the summary. */
async function managerNews(): Promise<{ briefs: Record<string, Brief>; entries: ManagerEntry[] }> {
  const briefs: Record<string, Brief> = {};
  let entries: ManagerEntry[] = [];
  try {
    const reply = await ask({ type: "manager_briefs" });
    for (const b of (reply.briefs as Brief[] | undefined) ?? []) briefs[b.sessionId] = b;
  } catch {
    // Without briefs the summary says what the agents said last.
  }
  try {
    const reply = await ask({ type: "manager_history", limit: 200 });
    entries = (reply.entries as ManagerEntry[] | undefined) ?? [];
  } catch {
    // Without them, no approvals are counted.
  }
  return { briefs, entries };
}

async function build(since: number, until: number): Promise<AwayDigest> {
  const news = daemon.status === "connected" ? await managerNews() : { briefs: {}, entries: [] };
  return makeDigest(since, until, events, getStore().sessions, news.briefs, news.entries);
}

/** Back: a summary, when the user was away long enough, wants one, and
 *  something happened meanwhile. */
async function cameBack() {
  const since = leftAt;
  leftAt = null;
  if (getPref<boolean>("awaySummary", true) === false || since === null) return;
  const minutes = getPref<number>("awayMinutes", 15);
  const now = Date.now();
  if (now - since < minutes * 60_000) return;
  const built = await build(since, now);
  if (!built.agents.length) return;
  digest = built;
  last = built;
  changed();
}

/** The summary since a time, on request. */
export async function showAwaySince(since: number) {
  const built = await build(since, Date.now());
  digest = built;
  last = built;
  changed();
}

/** The last summary's time again, else the past hour. */
export function showLastAway() {
  void showAwaySince(last?.since ?? Date.now() - 3600_000);
}

export function closeAway() {
  digest = null;
  changed();
}

export function useAwayDigest(): AwayDigest | null {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    () => digest,
  );
}

// What the agents do, as it happens: a turn finished, a question asked.
const questions = new Map<string, string | undefined>();
daemon.on((event) => {
  if (event.type === "sessions") {
    for (const s of event.sessions ?? []) questions.set(s.id, s.question?.id);
  } else if (event.type === "turn_complete" && event.sessionId) {
    record("finished", event.sessionId, event.preview);
  } else if (event.type === "session_update" && event.session) {
    const s = event.session;
    const before = questions.get(s.id);
    if (questions.has(s.id) && s.question && before !== s.question.id) record("needsYou", s.id, s.question.title);
    questions.set(s.id, s.question?.id);
  }
});

// Away: the app in the background, the screen locked, asleep. Unlocked
// with the app in front is back.
onActive((active) => (active ? void cameBack() : left()));
onAway((what) => {
  if (what === "unlock") {
    if (appActive()) void cameBack();
  } else left();
});
