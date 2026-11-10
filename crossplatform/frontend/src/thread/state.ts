// What a thread keeps beside the store's messages (AgentsStore.swift): the
// composer's error per agent, messages queued until a turn ends, Agentman's
// titles for runs of tool calls, the pictures read for the thread, and the
// drafts kept across launches.

import { useSyncExternalStore } from "react";
import { Call } from "@wailsio/runtime";
import type { Message, Tool } from "@shared/protocol";
import { daemon, type AppEvent, type AppRequest } from "../daemon";
import { getPref, setPref } from "../prefs";

type Listener = () => void;

/** A little observable value, by key, that only its readers redraw for. */
class Keyed<T> {
  private values = new Map<string, T>();
  private listeners = new Map<string, Set<Listener>>();

  get(key: string): T | undefined {
    return this.values.get(key);
  }

  set(key: string, value: T | undefined) {
    if (value === undefined) this.values.delete(key);
    else this.values.set(key, value);
    for (const l of this.listeners.get(key) ?? []) l();
  }

  use(key: string): T | undefined {
    return useSyncExternalStore(
      (cb) => {
        if (!this.listeners.has(key)) this.listeners.set(key, new Set());
        this.listeners.get(key)!.add(cb);
        return () => this.listeners.get(key)?.delete(cb);
      },
      () => this.values.get(key),
    );
  }
}

/** The daemon's words without the package that raised them. */
export function plainProblem(error: string | undefined, fallback: string): string {
  if (!error) return fallback;
  return error.replace(/^(daemon|source|tmux|workspace): /, "");
}

/** Sends a request and waits; an error event comes back as a thrown Error. */
export async function ask(req: AppRequest | (Record<string, unknown> & { type: string }), fallback = "That didn't work."): Promise<AppEvent> {
  const reply = await daemon.request(req as AppRequest);
  if (reply.type === "error") throw new Error(plainProblem(reply.error, fallback));
  return reply;
}

// MARK: the composer's error, per agent

const errors = new Keyed<string>();
export const setActionError = (sessionId: string, message: string | null | undefined) => errors.set(sessionId, message || undefined);
export const useActionError = (sessionId: string) => errors.use(sessionId);

// MARK: queued until the agent's turn ends

const queued = new Keyed<string[]>();
export const useQueued = (sessionId: string) => queued.use(sessionId) ?? [];

/** A queued message that has reached the agent shows as its own. */
export function dropDelivered(sessionId: string, messages: readonly Message[]) {
  const waiting = queued.get(sessionId);
  if (!waiting?.length) return;
  const left = waiting.filter((text) => {
    const want = text.trim();
    return !messages.some((m) => m.role === "user" && (m.text ?? "").includes(want));
  });
  if (left.length !== waiting.length) queued.set(sessionId, left);
}

/**
 * Sends a message, and images with it. Returns whether the daemon took it;
 * a refusal is the composer's error. Queued for the turn's end, it waits in
 * a dashed bubble.
 */
export async function send(sessionId: string, text: string, images: { mime: string; data: string }[] = []): Promise<boolean> {
  setActionError(sessionId, null);
  try {
    const reply = await daemon.request({
      type: "send_message", sessionId, text, clientId: `app-send-${crypto.randomUUID()}`,
      ...(images.length ? { images } : {}),
    } as AppRequest);
    if (reply.type === "error" || reply.status === "failed") {
      setActionError(sessionId, reply.error ?? "The agent didn't take that.");
      return false;
    }
    if (reply.status === "queued") {
      const note = images.length === 1 ? "1 image" : `${images.length} images`;
      queued.set(sessionId, [...(queued.get(sessionId) ?? []), images.length === 0 ? text : text ? `${note} · ${text}` : note]);
    }
    return true;
  } catch (err) {
    setActionError(sessionId, err instanceof Error ? err.message : String(err));
    return false;
  }
}

/** An action that answers with a send status (answer, send now). */
export async function perform(sessionId: string, req: Record<string, unknown> & { type: string }): Promise<boolean> {
  setActionError(sessionId, null);
  try {
    const reply = await daemon.request({ ...req, sessionId, clientId: `app-${req.type}-${crypto.randomUUID()}` } as AppRequest);
    if (reply.type === "error" || reply.status === "failed") {
      setActionError(sessionId, reply.error ?? "The agent didn't take that.");
      return false;
    }
    return true;
  } catch (err) {
    setActionError(sessionId, err instanceof Error ? err.message : String(err));
    return false;
  }
}

// MARK: Agentman's titles for runs of tool calls

const titles = new Keyed<string>();
const titling = new Set<string>();
const titleFailed = new Map<string, number>();

export function toolRunKey(calls: readonly Message[]): string {
  return `${calls[0]?.id ?? ""}|${calls.at(-1)?.id ?? ""}|${calls.length}`;
}

export const useToolTitle = (calls: readonly Message[]) => titles.use(toolRunKey(calls));

/** Asks Agentman for a plain line saying what a finished run did and why,
 *  once per run; again a minute after it failed. */
export async function titleTools(calls: readonly Message[], said: string) {
  const first = calls[0];
  if (calls.length < 2 || !first) return;
  const key = toolRunKey(calls);
  if (titles.get(key) || titling.has(key)) return;
  const failed = titleFailed.get(key);
  if (failed && Date.now() - failed < 60_000) return;
  titling.add(key);
  try {
    const steps = calls.map((c) => c.tool).filter((t): t is Tool => !!t);
    const reply = await daemon.request({ type: "tools_title", sessionId: first.sessionId, text: said, steps });
    const line = typeof reply.preview === "string" ? reply.preview : "";
    if (reply.type === "tools_title" && line) {
      titles.set(key, line);
      titleFailed.delete(key);
    } else {
      titleFailed.set(key, Date.now());
    }
  } catch {
    titleFailed.set(key, Date.now());
  } finally {
    titling.delete(key);
  }
}

// MARK: pictures

/** A picture's bytes as a data: URL, read once. */
const pictures = new Map<string, Promise<string | null>>();

/** An image the agent opened (or the daemon kept for a message), through
 *  read_seen_file: a page can't read the disk. */
export function seenImage(sessionId: string, path: string): Promise<string | null> {
  const key = `${sessionId}\u0000${path}`;
  let found = pictures.get(key);
  if (!found) {
    found = daemon
      .request({ type: "read_seen_file", sessionId, path })
      .then((reply) => {
        const image = reply.workspace?.image;
        if (!image) return null;
        return `data:${reply.workspace?.mime ?? "image/png"};base64,${image}`;
      })
      .catch(() => null);
    pictures.set(key, found);
    // A refusal is asked again next time: the daemon may not know the
    // agent's files yet.
    void found.then((url) => {
      if (!url) pictures.delete(key);
    });
  }
  return found;
}

const inShell = location.protocol === "wails:" || location.hostname === "wails.localhost";

export interface LocalFile {
  path: string;
  name: string;
  mime: string;
  size: number;
  /** base64, absent past the size asked for. */
  data: string;
}

/** A file on this computer, through the shell (menus.go Files.Read). */
export async function readLocalFile(path: string, max: number): Promise<LocalFile | null> {
  if (!inShell) return null;
  try {
    return (await Call.ByName("main.Files.Read", path, max)) as LocalFile;
  } catch {
    return null;
  }
}

const exists = new Map<string, boolean>();
let existsVersion = 0;
const existsListeners = new Set<Listener>();

/** Whether each path is a file of the user's (not a folder), as the Mac's
 *  FileManager check: assumed until the shell says otherwise. */
export function useAreFiles(paths: readonly string[]): (path: string) => boolean {
  useSyncExternalStore(
    (cb) => {
      existsListeners.add(cb);
      return () => existsListeners.delete(cb);
    },
    () => existsVersion,
  );
  for (const path of paths) {
    if (exists.has(path) || !inShell) continue;
    exists.set(path, true);
    void readLocalFile(path, 0).then((file) => {
      if (exists.get(path) === !!file) return;
      exists.set(path, !!file);
      existsVersion += 1;
      for (const l of existsListeners) l();
    });
  }
  return (path) => exists.get(path) ?? true;
}

// MARK: drafts

let drafts: Record<string, string> = getPref<Record<string, string>>("drafts", {});
let draftWrite: number | undefined;

export function draftFor(sessionId: string): string {
  return drafts[sessionId] ?? "";
}

/** Keeps a session's unsent message, or forgets it once empty; written once
 *  typing pauses, as the Mac writes them. */
export function saveDraft(sessionId: string, text: string, live: readonly string[] = []) {
  const next = { ...drafts };
  if (text.trim() === "") delete next[sessionId];
  else next[sessionId] = text;
  if (Object.keys(next).length > 50 && live.length) {
    for (const id of Object.keys(next)) if (!live.includes(id)) delete next[id];
  }
  drafts = next;
  window.clearTimeout(draftWrite);
  draftWrite = window.setTimeout(writeDrafts, 800);
}

export function writeDrafts() {
  window.clearTimeout(draftWrite);
  setPref("drafts", drafts);
}

window.addEventListener("beforeunload", writeDrafts);
