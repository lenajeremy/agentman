// What the Settings window reads from am serve and writes back, as the Mac
// app's ManagerStore and AgentsStore do for SettingsView: Agentman's
// settings, the notes, the keys, recent folders and which am serve this is.

import { useSyncExternalStore } from "react";
import type { DaemonInfo, Folder, Key, ManagerSettings, Note, PermissionCheck, VoiceAccount } from "@shared/protocol";
import { daemon, type AppEvent, type AppRequest } from "../daemon";

interface State {
  settings?: ManagerSettings;
  notes: Note[];
  keys: Key[];
  /** Folders agents ran in, most recent first. */
  folders: Folder[];
  daemonInfo?: DaemonInfo;
}

let state: State = { notes: [], keys: [], folders: [] };
const subscribers = new Set<() => void>();

function set(next: Partial<State>) {
  state = { ...state, ...next };
  for (const s of subscribers) s();
}

export function useSettingsState(): State {
  return useSyncExternalStore(
    (cb) => {
      subscribers.add(cb);
      return () => subscribers.delete(cb);
    },
    () => state,
  );
}

export function settingsState(): State {
  return state;
}

function apply(event: AppEvent) {
  switch (event.type) {
    case "sessions":
      if (event.daemon) set({ daemonInfo: event.daemon });
      break;
    case "manager_settings":
      if (event.managerSettings) set({ settings: event.managerSettings });
      break;
    case "keys":
      if (event.keys) set({ keys: event.keys });
      break;
  }
}

daemon.on((event) => apply(event));
daemon.onStatus((status) => {
  if (status === "connected") void reload();
});
if (daemon.status === "connected") void reload();

/** Resolves once connected to am serve, as the Mac's requests wait for the
 *  connection (up to the request's own time limit). */
function connected(): Promise<void> {
  if (daemon.status === "connected") return Promise.resolve();
  return new Promise((resolve, reject) => {
    const timer = window.setTimeout(() => {
      off();
      reject(new Error("not connected to am serve"));
    }, 30_000);
    const off = daemon.onStatus((status) => {
      if (status !== "connected") return;
      window.clearTimeout(timer);
      off();
      resolve();
    });
  });
}

/** A request, sent once connected. */
async function ask(req: AppRequest): Promise<AppEvent> {
  await connected();
  return daemon.request(req);
}

async function reload() {
  try {
    const reply = await daemon.request({ type: "manager_settings" });
    if (reply.managerSettings) set({ settings: reply.managerSettings });
  } catch {
    // Not connected yet: the next connection asks again.
  }
}

const unreachable = "Couldn't reach am serve.";

/** The settings the user edits, without what am serve reports (keys), so a
 *  key saved meanwhile is not taken for an unsaved change. As the Mac's
 *  ManagerSettings.editable, compared field by field. */
export function editable(s?: ManagerSettings): string {
  if (!s) return "";
  const copy: Record<string, unknown> = { ...s, keySet: false };
  delete copy.provider;
  delete copy.anthropicKeySet;
  delete copy.geminiKeySet;
  return stable(copy);
}

/** JSON with every object's keys in order, so equal settings read equal. */
function stable(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stable).join(",")}]`;
  if (value && typeof value === "object") {
    const entries = Object.entries(value).filter(([, v]) => v !== undefined && v !== null).sort(([a], [b]) => a.localeCompare(b));
    return `{${entries.map(([k, v]) => `${JSON.stringify(k)}:${stable(v)}`).join(",")}}`;
  }
  return JSON.stringify(value);
}

/** The engine a model runs on. */
export function providerOf(model: string): "gemini" | "anthropic" {
  return model.startsWith("gemini-") ? "gemini" : "anthropic";
}

/** Saves settings. Answers the problem, if any. */
export async function saveSettings(next: ManagerSettings): Promise<string | undefined> {
  try {
    const reply = await ask({ type: "manager_set_settings", manager: next });
    if (reply.type === "error") return reply.error;
    if (reply.managerSettings) set({ settings: reply.managerSettings });
    return undefined;
  } catch {
    return unreachable;
  }
}

/** Stores an engine's API key through am serve, or removes it when empty. */
export async function setKey(key: string, provider: string): Promise<string | undefined> {
  try {
    const req = { type: "manager_set_key", text: key, kind: provider === "gemini" ? "gemini" : undefined };
    const reply = await ask(req as unknown as AppRequest);
    if (reply.type === "error") return reply.error;
    if (reply.managerSettings) set({ settings: reply.managerSettings });
    return undefined;
  } catch {
    return unreachable;
  }
}

/** What Agentman would do with a request, under the settings as saved. */
export async function checkPermission(kind: string, target: string, folder: string): Promise<PermissionCheck | undefined> {
  try {
    const reply = await ask({ type: "manager_check_permission", kind, text: target, path: folder });
    return reply.permission;
  } catch {
    return undefined;
  }
}

export async function loadFolders() {
  try {
    const reply = await ask({ type: "list_folders" });
    if (reply.folders) set({ folders: [...reply.folders].sort((a, b) => (b.lastActivityAt ?? 0) - (a.lastActivityAt ?? 0)) });
  } catch {
    // Kept as they were.
  }
}

// Knowledge.

export async function loadNotes() {
  try {
    const reply = await ask({ type: "knowledge_list" });
    if (reply.notes) set({ notes: reply.notes });
  } catch {
    // Kept as they were.
  }
}

/** A note in full. */
export async function readNote(name: string, tier: string): Promise<Note | undefined> {
  try {
    return (await ask({ type: "knowledge_read", path: name, kind: tier })).note;
  } catch {
    return undefined;
  }
}

/** Saves a note: answers it as saved, or the problem. */
export async function writeNote(note: Note): Promise<{ note?: Note; problem?: string }> {
  try {
    const reply = await ask({ type: "knowledge_write", note });
    if (reply.type === "error" || !reply.note) return { problem: reply.error ?? "Couldn't save the note." };
    await loadNotes();
    return { note: reply.note };
  } catch {
    return { problem: unreachable };
  }
}

export async function deleteNote(note: Note): Promise<string | undefined> {
  try {
    const reply = await ask({ type: "knowledge_delete", path: note.name, kind: note.tier });
    if (reply.type === "error") return reply.error;
    if (reply.notes) set({ notes: reply.notes });
    return undefined;
  } catch {
    return unreachable;
  }
}

// Keys.

export async function loadKeys() {
  try {
    const reply = await ask({ type: "keys_list" });
    if (reply.keys) set({ keys: reply.keys });
  } catch {
    // Kept as they were.
  }
}

async function perform(req: AppRequest): Promise<string | undefined> {
  try {
    const reply = await ask(req);
    if (reply.type === "error") return reply.error ?? "That didn't work.";
    apply(reply);
    return undefined;
  } catch {
    return unreachable;
  }
}

/** Saves a key's details, and its value when one is given. */
export function saveKey(key: Key, value: string) {
  return perform({ type: "key_set", key, text: value || undefined });
}

export function deleteKey(key: Key) {
  return perform({ type: "key_delete", path: key.name });
}

// Voice prompts: the Celery sign-in, kept by am serve.

export interface VoiceSignIn {
  email?: string;
  requestId?: string;
  code?: string;
}

export type VoiceReply = VoiceAccount & { requestId?: string };

export async function voiceAccount(type = "voice_account", step?: VoiceSignIn): Promise<{ account?: VoiceReply; problem?: string }> {
  try {
    const req = { type, voiceSignIn: step } as unknown as AppRequest;
    const reply = await ask(req);
    if (reply.voiceAccount) return { account: reply.voiceAccount as VoiceReply };
    return { problem: String(reply.error ?? "Celery didn't answer.").replace(/^daemon: /, "") };
  } catch (err) {
    return { problem: err instanceof Error ? err.message : String(err) };
  }
}

// Languages: the language servers am serve runs for the editor.

export interface LanguageServer {
  id: string;
  name: string;
  language: string;
  /** yours, mac, downloaded, download, missing or off. */
  source: string;
  path?: string;
  size?: number;
  downloadSize?: string;
  missing?: string;
  running?: string[];
}

export interface LanguagesState {
  noDownloads: boolean;
  servers: LanguageServer[];
  /** The Node.js am downloaded, in bytes. */
  nodeSize?: number;
}

export interface LanguageChange {
  server?: string;
  path?: string;
  disabled?: boolean;
  noDownloads?: boolean;
}

/** Asks am serve about its language servers: answers the state or the problem. */
export async function languages(type: "lsp_status" | "lsp_set" | "lsp_remove", change?: LanguageChange): Promise<{ state?: LanguagesState; problem?: string }> {
  let reply: AppEvent | undefined;
  try {
    reply = await ask({ type, language: change } as unknown as AppRequest);
  } catch {
    reply = undefined;
  }
  if (reply?.languages) return { state: reply.languages as LanguagesState };
  if (reply?.error) {
    return { problem: reply.error.includes("unknown") ? "Language servers need a newer am serve: brew upgrade agentman" : reply.error };
  }
  return { problem: unreachable };
}
