// The agents and their conversations, kept from am serve's events.

import { useEffect, useState, useSyncExternalStore } from "react";
import type { Message, Session } from "@shared/protocol";
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
const asked = new Set<string>();
const subscribers = new Set<() => void>();
const subscribed = new Set<string>();

function set(next: Partial<State>) {
  state = { ...state, ...next };
  for (const s of subscribers) s();
}

function mergeMessages(sessionId: string, incoming: Message[]) {
  const byId = new Map((state.messages[sessionId] ?? []).map((m) => [m.id, m]));
  for (const m of incoming) byId.set(m.id, m);
  const merged = [...byId.values()].sort((a, b) => a.ts - b.ts);
  set({ messages: { ...state.messages, [sessionId]: merged } });
}

function apply(event: AppEvent) {
  switch (event.type) {
    case "sessions":
      set({ sessions: event.sessions ?? [], version: event.daemon?.version });
      for (const session of event.sessions ?? []) void workspace(session);
      break;
    case "session_update": {
      const updated = event.session;
      if (!updated) break;
      const others = state.sessions.filter((s) => s.id !== updated.id);
      set({ sessions: [...others, updated] });
      break;
    }
    case "session_gone":
      set({ sessions: state.sessions.filter((s) => s.id !== event.sessionId) });
      break;
    case "messages":
      if (event.sessionId && event.messages) mergeMessages(event.sessionId, event.messages);
      break;
    case "page":
      if (event.page) {
        mergeMessages(event.page.sessionId, event.page.messages);
        set({ hasMore: { ...state.hasMore, [event.page.sessionId]: event.page.hasMore } });
      }
      break;
  }
}

daemon.on((event) => apply(event));
daemon.onStatus((status) => {
  if (status !== "connected") return;
  daemon.send({ type: "list_sessions" });
  for (const id of subscribed) {
    daemon.send({ type: "subscribe", sessionId: id });
    daemon.send({ type: "fetch_messages", sessionId: id, limit: 200 });
  }
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
  subscribed.add(sessionId);
  daemon.send({ type: "subscribe", sessionId });
  daemon.send({ type: "fetch_messages", sessionId, limit: 200 });
  return () => {
    subscribed.delete(sessionId);
    daemon.send({ type: "unsubscribe", sessionId });
  };
}

export function loadOlder(sessionId: string) {
  const oldest = state.messages[sessionId]?.[0];
  if (!oldest) return;
  daemon.send({ type: "fetch_messages", sessionId, before: oldest.id, limit: 200 });
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
