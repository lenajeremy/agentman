// What the main window holds beside the agents themselves (AgentsStore.swift's
// search, filters, folders, renaming, palette): one small store, read with
// useShell(), changed with setShell() or the helpers below.

import { useSyncExternalStore } from "react";
import type { Folder, Session, SessionState } from "@shared/protocol";
import { daemon, type AppRequest } from "../daemon";

/** The states the sidebar filters by, as AgentsStore.filterState counts them. */
export type FilterState = Extract<SessionState, "waiting_input" | "busy" | "idle">;

export interface NewAgentPreset {
  folder: string;
  newWorktree: boolean;
}

export interface ShellState {
  search: string;
  stateFilter: FilterState | null;
  /** The folder the sidebar shows, with its subfolders. */
  folderFilter: string | null;
  /** Sessions recorded in the folder filtered to, running ones included. */
  pastSessions: Session[];
  /** Folders agents have run in, newest first. */
  folders: Folder[];
  /** The session being renamed in the sidebar. */
  renaming: string | null;
  showPalette: boolean;
  /** Where the New Agent page opens, when something sends the user there. */
  newAgentPreset: NewAgentPreset | null;
  /** Conversations opened from a search that aren't on any list, so their
   *  page has them (AgentsStore.foundSessions). */
  foundSessions: Record<string, Session>;
}

let state: ShellState = {
  search: "",
  stateFilter: null,
  folderFilter: null,
  pastSessions: [],
  folders: [],
  renaming: null,
  showPalette: false,
  newAgentPreset: null,
  foundSessions: {},
};
const listeners = new Set<() => void>();

export function getShell(): ShellState {
  return state;
}

export function setShell(next: Partial<ShellState>) {
  const folderChanged = "folderFilter" in next && next.folderFilter !== state.folderFilter;
  state = { ...state, ...next };
  for (const l of listeners) l();
  if (folderChanged) void loadPastSessions();
}

export function useShell(): ShellState {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    () => state,
  );
}

/** A request this app makes that the phone's protocol types don't list. */
export function ask(req: Record<string, unknown> & { type: string }) {
  return daemon.request(req as unknown as AppRequest);
}

/** Folders agents have run in, newest first. */
export async function loadFolders() {
  try {
    const reply = await ask({ type: "list_folders" });
    const folders = (reply.folders as Folder[] | undefined) ?? [];
    setShell({ folders: [...folders].sort((a, b) => (b.lastActivityAt ?? 0) - (a.lastActivityAt ?? 0)) });
  } catch {
    // Not connected: the list stays as it was.
  }
}

/** The sessions recorded in the folder filtered to, read-only until resumed. */
async function loadPastSessions() {
  const folder = state.folderFilter;
  if (!folder) {
    setShell({ pastSessions: [] });
    return;
  }
  try {
    const reply = await ask({ type: "directory_sessions", path: folder });
    if (reply.type === "error" || state.folderFilter !== folder) {
      if (state.folderFilter === folder) setShell({ pastSessions: [] });
      return;
    }
    const sessions = ((reply.sessions as Session[] | undefined) ?? []).sort((a, b) => (b.lastActivityAt ?? 0) - (a.lastActivityAt ?? 0));
    setShell({ pastSessions: sessions });
  } catch {
    setShell({ pastSessions: [] });
  }
}

// A folder filtered to before am serve could be asked (a launch option),
// or across a reconnect: its sessions read again.
daemon.onStatus((status) => {
  if (status === "connected" && state.folderFilter) void loadPastSessions();
});

/** The daemon's words without the package that raised them. */
export function plainProblem(error: unknown, fallback: string): string {
  const text = typeof error === "string" ? error : error instanceof Error ? error.message : "";
  if (!text) return fallback;
  return text.replace(/^(daemon|source|tmux|workspace): /, "");
}
