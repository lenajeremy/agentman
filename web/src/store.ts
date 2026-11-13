// The agents and their conversations, kept from am serve's events.

import { useEffect, useState, useSyncExternalStore } from "react";
import type { Message, Page, Session } from "@shared/protocol";
import { daemon, type AppEvent, type Status } from "./daemon";

interface State {
  sessions: Session[];
  messages: Record<string, Message[]>;
  hasMore: Record<string, boolean>;
  version?: string;
  /** The branch checked out in each folder, from am serve (not a git of our own). */
  branches: Record<string, string>;
  /** Lines added and removed in each agent's folder. */
  changes: Record<string, { added: number; removed: number }>;
}

let state: State = { sessions: [], messages: {}, hasMore: {}, branches: {}, changes: {} };

// Whether the daemon has listed its sessions yet: until then an agent that
// isn't there says nothing about whether it ended, and an empty Board says
// nothing about whether there are agents.
let listed = false;
daemon.on((e) => {
  if (e.type === "sessions") listed = true;
});
export const sessionsLoaded = () => listed;
const asked = new Set<string>();
const subscribers = new Set<() => void>();
const subscribed = new Set<string>();

function set(next: Partial<State>) {
  state = { ...state, ...next };
  for (const s of subscribers) s();
}

function mergeMessages(sessionId: string, incoming: Message[]) {
  const byId = new Map((state.messages[sessionId] ?? []).map((m) => [m.id, m]));
  for (const m of incoming) {
    // A later copy replaces the earlier one (a running call arriving
    // finished), never the other way: a page read before the result was
    // written must not set a finished call running again.
    const known = byId.get(m.id);
    if (known?.tool && known.tool.status !== "running" && m.tool?.status === "running") continue;
    byId.set(m.id, m);
  }
  const merged = [...byId.values()].sort((a, b) => a.ts - b.ts);
  set({ messages: { ...state.messages, [sessionId]: merged } });
}

type Left = (before: readonly Session[], now: readonly Session[]) => void;
const leftListeners = new Set<Left>();

/** Called once agents may have left the list (a new list, or one gone),
 *  with the list before and after. */
export function onSessionsLeft(listener: Left): () => void {
  leftListeners.add(listener);
  return () => leftListeners.delete(listener);
}

function apply(event: AppEvent) {
  switch (event.type) {
    case "sessions": {
      const before = state.sessions;
      set({ sessions: event.sessions ?? [], version: event.daemon?.version });
      for (const session of event.sessions ?? []) void workspace(session);
      for (const listener of leftListeners) listener(before, state.sessions);
      break;
    }
    case "session_update": {
      const updated = event.session;
      // One without its id can't be placed, and drawn it would take the page
      // down; am serve always sends one.
      if (!updated?.id) break;
      // Where it was, as the Mac keeps it: agents active in the same moment
      // stay in the order am serve listed them.
      const at = state.sessions.findIndex((s) => s.id === updated.id);
      set({ sessions: at < 0 ? [...state.sessions, updated] : state.sessions.map((s, i) => (i === at ? updated : s)) });
      break;
    }
    case "session_gone": {
      const before = state.sessions;
      set({ sessions: state.sessions.filter((s) => s.id !== event.sessionId) });
      for (const listener of leftListeners) listener(before, state.sessions);
      break;
    }
    case "messages":
      if (event.sessionId && event.messages) mergeMessages(event.sessionId, event.messages);
      break;
    case "page":
      // Read where they are asked for: fetchPages, loadOlder.
      break;
  }
}

daemon.on((event) => apply(event));
daemon.onStatus((status) => {
  if (status !== "connected") return;
  daemon.send({ type: "list_sessions" });
  // A reconnect starts the daemon's side afresh: follow again.
  for (const id of subscribed) void fetchPages(id);
});

export function useStore(): State {
  return useSyncExternalStore(
    (cb) => {
      subscribers.add(cb);
      return () => subscribers.delete(cb);
    },
    () => state,
  );
}

export function useStatus(): { status: Status; detail: string } {
  const [s, setS] = useState({ status: daemon.status, detail: daemon.detail });
  useEffect(() => {
    const off = daemon.onStatus((status, detail = "") => setS({ status, detail }));
    // The connection may have opened between the first render and now.
    setS({ status: daemon.status, detail: daemon.detail });
    return off;
  }, []);
  return s;
}

/** Follows one agent's conversation while it is on screen. */
export function follow(sessionId: string): () => void {
  // Counted: the Board's cards and a thread can follow one agent at once,
  // and the first to stop mustn't stop it for the other.
  const count = (followers.get(sessionId) ?? 0) + 1;
  followers.set(sessionId, count);
  if (count === 1) {
    subscribed.add(sessionId);
    void fetchPages(sessionId);
  }
  let done = false;
  return () => {
    if (done) return;
    done = true;
    const left = (followers.get(sessionId) ?? 1) - 1;
    if (left > 0) {
      followers.set(sessionId, left);
      return;
    }
    followers.delete(sessionId);
    subscribed.delete(sessionId);
    daemon.send({ type: "unsubscribe", sessionId });
  };
}

const followers = new Map<string, number>();

/** Where each conversation's next older page starts, from the last page read. */
const cursors: Record<string, string | undefined> = {};
const loadingOlder = new Set<string>();
/** A page, and the most a catch-up reads, as the Mac's and the phone's (catchup.ts). */
const PAGE = 40;
const CATCH_UP_PAGES = 4;

/** Subscribes and reads the newest page. Followed again after a reconnect,
 *  the thread already shows what came before: the pages reach back to that,
 *  four at most, so nothing in between is missing. */
async function fetchPages(sessionId: string) {
  daemon.send({ type: "subscribe", sessionId });
  const shown = state.messages[sessionId] ?? [];
  const since = shown.length ? Math.max(...shown.map((m) => m.ts)) : undefined;
  try {
    let page = (await daemon.request({ type: "fetch_messages", sessionId, limit: PAGE })).page;
    if (!page) return;
    mergeMessages(sessionId, page.messages);
    let pages = 1;
    while (since !== undefined && page.hasMore && pages < CATCH_UP_PAGES && page.nextCursor &&
      page.messages.length > 0 && Math.min(...page.messages.map((m) => m.ts)) > since) {
      const next: Page | undefined = (await daemon.request({ type: "fetch_messages", sessionId, before: page.nextCursor, limit: PAGE })).page;
      if (!next) break;
      mergeMessages(sessionId, next.messages);
      page = next;
      pages += 1;
    }
    cursors[sessionId] = page.nextCursor;
    set({ hasMore: { ...state.hasMore, [sessionId]: page.hasMore } });
  } catch {
    // Not connected: the reconnect reads it again.
  }
}

/** The page before the oldest message shown, 40 at most. */
/** Reads a conversation back until a message is in it, as scrolling up
 *  would, and says whether it got there (SearchStore.loadThrough). */
export async function loadThrough(sessionId: string, messageId: string): Promise<boolean> {
  const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));
  // The newest page has to have come first.
  for (let i = 0; i < 80 && state.hasMore[sessionId] === undefined; i++) await wait(100);
  // A thousand pages back is far past where anyone searches, and keeps a
  // vanished message from reading forever.
  for (let i = 0; i < 1000; i++) {
    if ((state.messages[sessionId] ?? []).some((m) => m.id === messageId)) return true;
    if (!state.hasMore[sessionId]) return false;
    if (loadingOlder.has(sessionId)) await wait(50);
    else await loadOlder(sessionId);
  }
  return false;
}

/** The thread as loaded, and where its older pages start: where ↑ in the
 *  message box begins (AgentsStore.loadedPrompts). */
export function loadedThread(sessionId: string): { messages: readonly Message[]; cursor?: string; hasMore: boolean } {
  return { messages: state.messages[sessionId] ?? [], cursor: cursors[sessionId], hasMore: !!state.hasMore[sessionId] };
}

export async function loadOlder(sessionId: string): Promise<void> {
  const cursor = cursors[sessionId];
  if (!state.hasMore[sessionId] || !cursor || loadingOlder.has(sessionId)) return;
  loadingOlder.add(sessionId);
  try {
    const page = (await daemon.request({ type: "fetch_messages", sessionId, before: cursor, limit: PAGE })).page;
    if (!page) return;
    mergeMessages(sessionId, page.messages);
    cursors[sessionId] = page.nextCursor;
    set({ hasMore: { ...state.hasMore, [sessionId]: page.hasMore } });
  } catch {
    // Asked again when the reader comes back to the top.
  } finally {
    loadingOlder.delete(sessionId);
  }
}

export function sendMessage(sessionId: string, text: string) {
  return daemon.request({ type: "send_message", sessionId, text, clientId: crypto.randomUUID() });
}

/** The folder's branch and changes, asked once per agent per connection. */
async function workspace(session: Session) {
  if (asked.has(session.id)) return;
  asked.add(session.id);
  try {
    const commits = await daemon.request({ type: "list_commits", sessionId: session.id, limit: 1 });
    const branch = commits.workspace?.branch;
    if (branch) set({ branches: { ...state.branches, [session.cwd]: branch } });
    const changed = await daemon.request({ type: "list_changes", sessionId: session.id });
    const list = changed.workspace?.changes ?? [];
    const totals = list.reduce((t, c) => ({ added: t.added + (c.added ?? 0), removed: t.removed + (c.removed ?? 0) }), { added: 0, removed: 0 });
    set({ changes: { ...state.changes, [session.id]: totals } });
  } catch {
    asked.delete(session.id);
  }
}

daemon.onStatus((status) => {
  if (status !== "connected") asked.clear();
});

/** The branch in every live agent's folder, read again (Sidebar.swift reads
 *  them every 10 s while it shows); folders without one drop out. */
export async function refreshBranches() {
  const folders = new Map<string, string>();
  for (const s of state.sessions) if (s.state !== "ended" && !folders.has(s.cwd)) folders.set(s.cwd, s.id);
  const read: Record<string, string> = {};
  await Promise.all([...folders].map(async ([cwd, id]) => {
    try {
      const commits = await daemon.request({ type: "list_commits", sessionId: id, limit: 1 });
      const branch = commits.workspace?.branch;
      if (branch) read[cwd] = branch;
    } catch {
      // Not read this time: kept until the next round.
      if (state.branches[cwd]) read[cwd] = state.branches[cwd];
    }
  }));
  const same = Object.keys(read).length === Object.keys(state.branches).length && Object.entries(read).every(([k, v]) => state.branches[k] === v);
  if (!same) set({ branches: read });
}

/** The agents as they are now, for code outside React. */
export function getStore(): State {
  return state;
}
