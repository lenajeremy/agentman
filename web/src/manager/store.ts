// The conversation with Agentman, as am serve tells it (Stores/ManagerStore.swift):
// entries in order, replies streaming in, what Agentman is doing, its
// settings, briefs and schedules. And what the Mac's AgentsStore does with
// Agentman's news: seen at once while the conversation is on screen, a
// toast while the app is in front, a notification otherwise; the dock's
// count; and telling am serve which agents are on screen.

import { useSyncExternalStore } from "react";
import type { Brief, ManagerCard, ManagerEntry, ManagerSchedule, ManagerSettings, ManagerStatus, Note, Session } from "@shared/protocol";
import { applyDelta, cardIsOpen, isNews, prependOlder, upsertEntry } from "@shared/manager-feed";
import { daemon, type AppEvent, type AppRequest } from "../daemon";
import { clearAllNotifications, clearNotification, notifyWith, setBadge } from "../host";
import { getPref, setPref } from "../prefs";
import { shouldNotify, type NotificationKind } from "../notify/policy";
import type { Route } from "../nav";

/** One piece of news in the window's corner (AgentmanToasts); its id is
 *  the entry's. */
export interface Toast {
  id: string;
  sessionId?: string;
  card: boolean;
}

export interface ManagerState {
  entries: ManagerEntry[];
  hasOlder: boolean;
  loadingOlder: boolean;
  status: ManagerStatus;
  settings?: ManagerSettings;
  briefs: Record<string, Brief>;
  schedules: ManagerSchedule[];
  /** The last thing that went wrong talking to Agentman. */
  problem?: string;
  loaded: boolean;
  /** When the user last saw the conversation, in milliseconds. */
  lastRead: number;
  /** Newest last, three at most. */
  toasts: Toast[];
  /** The app is the one in front. */
  appActive: boolean;
  /** Where the main window is, as its toasts last saw it. */
  route: Route;
  /** Images read so far, as data: URLs, by "kept\0name" or "session\0path". */
  images: Record<string, string | null>;
  /** Agentman's conversation open beside the Board. */
  boardChatOpen: boolean;
}

const LAST_READ = "managerLastRead";
const MAX_TOASTS = 3;
/** Entries asked for at a time. */
const PAGE = 80;

// The first launch starts with nothing unread, not the whole history.
const storedLastRead = getPref<number | null>(LAST_READ, null);
const firstLastRead = storedLastRead ?? Date.now();
if (storedLastRead === null) setPref(LAST_READ, firstLastRead);

let state: ManagerState = {
  entries: [],
  hasOlder: false,
  loadingOlder: false,
  status: { state: "idle", keySet: false },
  briefs: {},
  schedules: [],
  loaded: false,
  lastRead: firstLastRead,
  toasts: [],
  appActive: document.hasFocus(),
  route: { kind: "agentman" },
  images: {},
  boardChatOpen: false,
};
const listeners = new Set<() => void>();
/** The agents, from the main window, to name them in notifications. */
let sessions: Session[] = [];
/** Set once the main window's toasts are up: only that window tells the
 *  user anything, never an editor's or Settings. */
let routing = false;

function set(next: Partial<ManagerState>) {
  state = { ...state, ...next };
  for (const l of listeners) l();
}

export function getManager(): ManagerState {
  return state;
}

export function useManager(): ManagerState {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    () => state,
  );
}

// MARK: - Reading

export const thinking = (s: ManagerState = state) => s.status.state === "thinking";

/** Agentman can't take a message until it has its key. */
export const needsKey = (s: ManagerState = state) => (s.settings ? !s.settings.keySet : s.status.state === "needs_key");

/** The engine Agentman's conversation runs on. */
export function provider(s: ManagerState = state): string {
  if (s.settings?.provider) return s.settings.provider;
  if (s.settings) return s.settings.chatModel.startsWith("gemini-") ? "gemini" : "anthropic";
  return "anthropic";
}

/** The entry page: only while there is no conversation. */
export const showsEntry = (s: ManagerState = state) => s.entries.length === 0 && !thinking(s);

/** Cards still waiting on the user. */
export function openCardsOf(s: ManagerState = state): ManagerCard[] {
  return s.entries.flatMap((e) => (e.card && cardIsOpen(e.card) ? [e.card] : []));
}

/** Notes Agentman suggests, waiting for the user. */
export const noteSuggestions = (s: ManagerState = state) => openCardsOf(s).filter((c) => c.kind === "note");

/** What arrived since the user last looked. */
export function unreadOf(s: ManagerState = state): ManagerEntry[] {
  return s.entries.filter((e) => e.ts > s.lastRead && !e.closed && isNews(e));
}

/** The entry with this id, as it is now. */
export function entryById(id: string, s: ManagerState = state): ManagerEntry | undefined {
  for (let i = s.entries.length - 1; i >= 0; i--) if (s.entries[i].id === id) return s.entries[i];
  return undefined;
}

/** Whether the user is looking at the conversation with Agentman: open
 *  beside the Board, as the Mac's viewingManager. */
export const viewingManager = (s: ManagerState = state) => s.route.kind === "agentman" && s.boardChatOpen && s.appActive;

/** Opens or closes Agentman's conversation beside the Board. */
export function setBoardChatOpen(open: boolean) {
  if (open === state.boardChatOpen) return;
  set({ boardChatOpen: open });
  seeManagerIfShown();
  tellOnScreen();
}

function looking(sessionId: string): boolean {
  return state.appActive && state.route.kind === "agent" && state.route.id === sessionId;
}

export function sessionById(id?: string): Session | undefined {
  return id ? sessions.find((s) => s.id === id) : undefined;
}

// MARK: - Loading

/** Loads everything afresh: on connecting, and after a reconnect. */
export async function reload() {
  const ask = (req: AppRequest) => daemon.request(req).catch(() => undefined);
  const [settings, briefs, schedules, history] = await Promise.all([
    ask({ type: "manager_settings" }),
    ask({ type: "manager_briefs" }),
    ask({ type: "manager_schedules" }),
    ask({ type: "manager_history", limit: PAGE }),
  ]);
  const next: Partial<ManagerState> = { loaded: true };
  if (settings?.managerSettings) next.settings = settings.managerSettings;
  if (settings?.managerStatus) next.status = settings.managerStatus;
  if (briefs?.briefs) next.briefs = Object.fromEntries(briefs.briefs.map((b) => [b.sessionId, b]));
  if (history?.type === "manager_history") {
    next.entries = history.entries ?? [];
    next.hasOlder = history.more ?? false;
  }
  if (schedules?.type === "manager_schedules") next.schedules = schedules.schedules ?? [];
  set(next);
}

export async function loadOlder() {
  const first = state.entries[0];
  if (!state.hasOlder || state.loadingOlder || !first) return;
  set({ loadingOlder: true });
  try {
    const reply = await daemon.request({ type: "manager_history", before: first.id, limit: PAGE });
    if (reply.type === "manager_history" && reply.entries) {
      set({ entries: prependOlder(state.entries, reply.entries), hasOlder: reply.more ?? false });
    }
  } catch {
    // Asked again the next time the reader reaches the top.
  } finally {
    set({ loadingOlder: false });
  }
}

// MARK: - Talking to Agentman

export interface InlineImage {
  mime: string;
  data: string;
}

export async function send(text: string, images: InlineImage[] = []): Promise<boolean> {
  set({ problem: undefined });
  try {
    const req = { type: "manager_send", text, ...(images.length ? { images } : {}) } as unknown as AppRequest;
    const reply = await daemon.request(req);
    if (reply.type === "error") {
      set({ problem: reply.error });
      return false;
    }
    return true;
  } catch (err) {
    set({ problem: err instanceof Error ? err.message : String(err) });
    return false;
  }
}

export function stop() {
  daemon.send({ type: "manager_stop" });
}

export function setProblem(problem?: string) {
  set({ problem });
}

/** Starts a new conversation; the cards still waiting carry over. */
export async function reset(): Promise<string | undefined> {
  let reply: AppEvent;
  try {
    reply = await daemon.request({ type: "manager_reset" });
  } catch {
    return "Couldn't reach am serve.";
  }
  if (reply.type === "error") return reply.error;
  await reload();
  return undefined;
}

async function perform(req: AppRequest): Promise<string | undefined> {
  let reply: AppEvent;
  try {
    reply = await daemon.request(req);
  } catch {
    return "Couldn't reach am serve.";
  }
  if (reply.type === "error") return reply.error || "That didn't work.";
  return undefined;
}

/** Stores an engine's API key in the Keychain, through the daemon. */
export function setKey(key: string, engine = "anthropic"): Promise<string | undefined> {
  return perform({ type: "manager_set_key", text: key, ...(engine === "gemini" ? { kind: "gemini" } : {}) } as unknown as AppRequest);
}

export interface QuestionReply {
  optionKey?: string;
  optionKeys?: string[];
  text?: string;
}

/** Answers a card: the agent's question, or a reply to an agent waiting on
 *  the user. Returns the problem, if any. */
export function replyToCard(card: ManagerCard, reply: QuestionReply): Promise<string | undefined> {
  if (card.kind === "question" || card.kind === "key") {
    return perform({ type: "manager_card_reply", cardId: card.id, optionKey: reply.optionKey, optionKeys: reply.optionKeys, answerText: reply.text });
  }
  return perform({ type: "manager_card_reply", cardId: card.id, text: reply.text });
}

/** Saves a suggested note, as Agentman wrote it or as the user edited it. */
export function saveNote(card: ManagerCard, edited?: Note): Promise<string | undefined> {
  return perform({ type: "manager_card_reply", cardId: card.id, ...(edited ? { note: edited } : {}) });
}

/** Takes a brief, an agent's word or a card out of the conversation. */
export function closeEntry(entry: ManagerEntry): Promise<string | undefined> {
  return perform({ type: "manager_entry_close", entryId: entry.id });
}

/** Puts a card away unanswered. */
export function dismissCard(card: ManagerCard): Promise<string | undefined> {
  return perform({ type: "manager_card_dismiss", cardId: card.id });
}

/** Cancels one of Agentman's reminders or watches. */
export async function cancelSchedule(schedule: ManagerSchedule): Promise<string | undefined> {
  let reply: AppEvent;
  try {
    reply = await daemon.request({ type: "manager_schedule_cancel", scheduleId: schedule.id });
  } catch {
    return "Couldn't reach am serve.";
  }
  if (reply.type === "error") return reply.error;
  set({ schedules: reply.schedules ?? [] });
  return undefined;
}

function dataURL(base64: string, mime?: string, name = ""): string {
  const type = mime || (/\.jpe?g$/i.test(name) ? "image/jpeg" : /\.gif$/i.test(name) ? "image/gif" : /\.webp$/i.test(name) ? "image/webp" : "image/png");
  return `data:${type};base64,${base64}`;
}

/** An image the user sent Agentman, kept with the conversation. */
export async function keptImage(name: string): Promise<string | null> {
  const key = `kept\0${name}`;
  if (state.images[key]) return state.images[key];
  try {
    const reply = await daemon.request({ type: "manager_image", path: name });
    const image = reply.workspace?.image;
    if (!image) return null;
    const url = dataURL(image, reply.workspace?.mime, name);
    set({ images: { ...state.images, [key]: url } });
    return url;
  } catch {
    return null;
  }
}

/** An image an agent opened, for showing what Agentman looked at. */
export async function seenImage(sessionId: string, path: string): Promise<string | null> {
  const key = `${sessionId}\0${path}`;
  if (state.images[key]) return state.images[key];
  try {
    const reply = await daemon.request({ type: "read_seen_file", sessionId, path });
    const image = reply.workspace?.image;
    if (!image) return null;
    const url = dataURL(image, reply.workspace?.mime, path);
    set({ images: { ...state.images, [key]: url } });
    return url;
  } catch {
    return null;
  }
}

// MARK: - Events

function apply(event: AppEvent) {
  switch (event.type) {
    case "manager_entry":
      if (event.entry) upsert(event.entry);
      break;
    case "manager_delta":
      if (event.entryId && event.delta) set({ entries: applyDelta(state.entries, event.entryId, event.delta) });
      break;
    case "manager_status": {
      const status = event.managerStatus;
      if (!status) break;
      const finished = state.status.state === "thinking" && status.state !== "thinking";
      set({ status });
      // One notice per reply, when it is done, with its last words.
      if (finished) {
        const reply = [...state.entries].reverse().find((e) => e.role === "assistant" && e.text);
        if (reply && reply.ts > state.lastRead) news(reply);
      }
      break;
    }
    case "manager_settings":
      set({
        ...(event.managerSettings ? { settings: event.managerSettings } : {}),
        ...(event.managerStatus ? { status: event.managerStatus } : {}),
      });
      break;
    case "manager_reset":
      // A new conversation, started here or on the phone.
      void reload();
      break;
    case "manager_schedules":
      set({ schedules: event.schedules ?? [] });
      break;
    case "manager_brief": {
      const briefs = { ...state.briefs };
      if (event.brief) briefs[event.brief.sessionId] = event.brief;
      for (const b of event.briefs ?? []) briefs[b.sessionId] = b;
      set({ briefs });
      break;
    }
  }
}

/** Adds an entry, or replaces the one with its id, and says what changed:
 *  a card that no longer needs the user, or news. */
function upsert(entry: ManagerEntry) {
  const before = state.entries.find((e) => e.id === entry.id);
  const entries = upsertEntry(state.entries, entry);
  if (entries === state.entries) return;
  set({ entries });
  if (before) {
    const card = entry.card;
    if (card && cardIsOpen(before.card) && !cardIsOpen(card)) {
      cardClosed(card);
    } else if (card && cardIsOpen(card) && before.card?.suggested !== card.suggested) {
      // Agentman marked its pick: the user has something to confirm.
      news(entry);
    }
  } else if (entry.role === "brief" || entry.role === "agent" || cardIsOpen(entry.card)) {
    news(entry);
  }
}

// MARK: - News

/** News from Agentman: seen at once if the user is looking, a toast while
 *  the app is in front, a notification if not. */
function news(entry: ManagerEntry) {
  if (!routing) return;
  if (viewingManager()) {
    markRead();
  } else if (entry.sessionId && looking(entry.sessionId)) {
    // About the agent in front of the user: nothing to tell.
  } else if (state.appActive && toastWorthy(entry)) {
    showToast(entry);
  } else {
    const agent = sessionById(entry.sessionId);
    announce(entry, agent?.name, agent?.cwd);
  }
  refreshBadge();
}

/** What becomes a toast while the app is in front: an agent waiting on
 *  the user, its news, word from it, and Agentman's reply. A note or a key
 *  Agentman asks about stays a notification. */
export function toastWorthy(entry: ManagerEntry): boolean {
  if (entry.card) return cardIsOpen(entry.card) && (entry.card.kind === "question" || entry.card.kind === "needs_you");
  return entry.role === "brief" || entry.role === "agent" || entry.role === "assistant";
}

export function showToast(entry: ManagerEntry) {
  const toasts = state.toasts.filter((t) => t.id !== entry.id);
  toasts.push({ id: entry.id, sessionId: entry.sessionId, card: !!entry.card });
  if (toasts.length > MAX_TOASTS) {
    // News goes before a card that still waits on the user.
    const index = toasts.findIndex((t) => !t.card);
    toasts.splice(index >= 0 ? index : 0, 1);
  }
  set({ toasts });
}

export function dismissToast(id: string) {
  if (state.toasts.some((t) => t.id === id)) set({ toasts: state.toasts.filter((t) => t.id !== id) });
}

/** A card no longer needs the user, wherever it was answered: its
 *  notification and its toast go. */
function cardClosed(card: ManagerCard) {
  void clearNotification(card.id);
  const toasts = state.toasts.filter((t) => entryById(t.id)?.card?.id !== card.id);
  if (toasts.length !== state.toasts.length) set({ toasts });
  refreshBadge();
}

/** The user has seen everything so far. */
export function markRead() {
  const latest = state.entries[state.entries.length - 1]?.ts;
  if (latest === undefined || latest <= state.lastRead) return;
  setPref(LAST_READ, latest);
  set({ lastRead: latest });
}

function seeManagerIfShown() {
  if (!routing || !viewingManager()) return;
  // Agentman's conversation shows all of it.
  if (state.toasts.length) set({ toasts: [] });
  markRead();
  void clearAllNotifications();
  refreshBadge();
}

let badge = "";
function refreshBadge() {
  if (!routing) return;
  const count = unreadOf().length;
  const label = count > 0 ? String(count) : "";
  if (label === badge) return;
  badge = label;
  void setBadge(label);
}

/** Markdown flattened to a line of plain text, for a notification or a
 *  toast (Notifier.plain). */
export function plain(markdown: string): string {
  let text = markdown;
  for (const mark of ["**", "__", "`", "#"]) text = text.split(mark).join("");
  return text
    .split("\n")
    .map((line) => {
      let l = line.trim();
      for (const bullet of ["- ", "* ", "• "]) if (l.startsWith(bullet)) l = l.slice(bullet.length);
      return l;
    })
    .filter(Boolean)
    .join(" ");
}

/** Tells the user about news from Agentman (Notifier.announce). */
function announce(entry: ManagerEntry, agentName?: string, folder?: string) {
  const card = entry.card;
  const kind: NotificationKind = card ? (card.kind === "note" ? "noteSuggestion" : "needsYou") : "agentman";
  if (!shouldNotify(kind, folder)) return;
  let id = entry.id;
  let link = "agentman://home/conversation";
  let title: string;
  let body: string;
  let subtitle: string | undefined;
  let silent = false;
  if (card && card.kind === "note") {
    id = card.id;
    title = "Agentman suggests a note";
    body = card.note?.title ?? card.title;
    silent = true;
  } else if (card) {
    id = card.id;
    title = `${agentName ?? card.agentName} needs you`;
    body = [card.title, card.detail].filter((s): s is string => !!s).join("\n");
    if (card.suggested !== undefined) subtitle = "Agentman marked its pick for you to confirm";
  } else if (entry.role === "brief" || entry.role === "agent") {
    title = entry.role === "agent" ? `${agentName ?? "An agent"} says` : "Agentman";
    body = entry.text;
    if (entry.sessionId) link = `agentman://agent/${encodeURIComponent(entry.sessionId)}`;
  } else {
    title = "Agentman";
    body = plain(entry.text);
  }
  void notifyWith({ id, title, subtitle, body: [...body].slice(0, 400).join(""), link, thread: "agentman", silent });
}

// MARK: - What is on screen

/** This app, to the daemon, for as long as it runs. */
const viewerId = `app-${crypto.randomUUID()}`;
let told: string[] | null = null;
let renewal: number | undefined;

/** The agents whose threads are in front of the user. */
function onScreen(): string[] {
  if (!state.appActive) return [];
  return state.route.kind === "agent" ? [state.route.id] : [];
}

/** Tells Agentman what is on screen when it changes, so an agent the user
 *  is looking at gets no card and no push; said again every minute while
 *  anything is shown, as Agentman forgets after two. */
function tellOnScreen(again = false) {
  if (!routing) return;
  const shown = onScreen();
  if (!again && told && shown.join("\n") === told.join("\n")) return;
  told = shown;
  daemon.send({ type: "manager_viewing", clientId: viewerId, viewing: shown } as unknown as AppRequest);
  window.clearInterval(renewal);
  renewal = undefined;
  if (shown.length) renewal = window.setInterval(() => tellOnScreen(true), 60_000);
}

// MARK: - The main window

/** The main window's toasts say where it is and which agents there are;
 *  from then on this window is the one that tells the user anything. */
export function syncMainWindow(route: Route, agents: Session[]) {
  sessions = agents;
  const first = !routing;
  routing = true;
  const old = state.route;
  const changed = old.kind !== route.kind || (old.kind === "agent" && route.kind === "agent" && old.id !== route.id);
  if (changed) set({ route });
  if (first || changed) {
    seeManagerIfShown();
    tellOnScreen();
    // Opened, an agent's own thread says what its toasts did.
    if (route.kind === "agent" && state.toasts.some((t) => t.sessionId === route.id)) {
      set({ toasts: state.toasts.filter((t) => t.sessionId !== route.id) });
    }
    refreshBadge();
  }
}

window.addEventListener("focus", () => {
  set({ appActive: true });
  seeManagerIfShown();
  tellOnScreen();
});
window.addEventListener("blur", () => {
  set({ appActive: false });
  tellOnScreen();
});

daemon.on((event) => apply(event));
daemon.onStatus(async (status) => {
  if (status !== "connected") return;
  await reload();
  seeManagerIfShown();
  refreshBadge();
  // A reconnect starts the daemon's side afresh: say again what is on screen.
  tellOnScreen(true);
});
if (daemon.status === "connected") void reload();
