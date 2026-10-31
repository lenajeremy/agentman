// The agents and their conversations, kept from am serve's events.

import { useEffect, useState, useSyncExternalStore } from "react";
import type { Message, Session } from "@shared/protocol";
import { daemon, type AppEvent, type Status } from "./daemon";

interface State {
  sessions: Session[];
  messages: Record<string, Message[]>;
  hasMore: Record<string, boolean>;
  version?: string;
}

let state: State = { sessions: [], messages: {}, hasMore: {} };
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
  useEffect(() => daemon.onStatus((status, detail = "") => setS({ status, detail })), []);
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
