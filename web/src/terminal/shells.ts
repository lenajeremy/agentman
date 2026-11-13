// A folder's shells, as am serve keeps them (internal/term's registry): the
// Mac's EditorStore terminals, which the main window and the folder's editor
// share. Here the daemon owns them, so every window sees the same tabs and
// the shells outlive the panel and the window until the app quits.
//
// One watch socket per folder per window, opened while anything shows the
// folder's terminal: it sends the shells, the one in front and the split tabs
// whenever they change, and takes the commands below.

import { useEffect, useSyncExternalStore } from "react";
import { endpoint } from "../host";

export interface Shell {
  id: string;
  /** The shell's name, or the title a program in it set. */
  title: string;
  exited: boolean;
  code?: number;
  /** An agent's own terminal: the session it attaches to. */
  session?: string;
}

export interface FolderShells {
  /** False until am serve has said what the folder has. */
  ready: boolean;
  shells: Shell[];
  /** The shell in front: a tab's, the left half when it is split. */
  active: string;
  /** Tabs split in two: each left half's id, and the shell beside it. */
  splits: Record<string, string>;
  /** Why the folder's shells can't be had (a folder that has gone). */
  problem?: string;
}

export type ShellCommand =
  | { type: "new" }
  | { type: "ensure" }
  | { type: "agent"; session: string; title: string }
  | { type: "split" }
  | { type: "unsplit" }
  | { type: "close"; id: string }
  | { type: "restart"; id: string }
  | { type: "activate"; id: string };

const EMPTY: FolderShells = { ready: false, shells: [], active: "", splits: {} };

class Watch {
  state: FolderShells = EMPTY;
  private ws?: WebSocket;
  private queue: ShellCommand[] = [];
  private listeners = new Set<() => void>();
  private users = 0;
  private closeTimer?: number;
  private backoff = 500;
  private stopped = true;

  constructor(private folder: string) {}

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  /** Someone shows the folder's terminal; the socket opens for the first. */
  use(): () => void {
    this.users += 1;
    window.clearTimeout(this.closeTimer);
    if (this.stopped) {
      this.stopped = false;
      void this.open();
    }
    return () => {
      this.users -= 1;
      // A moment's grace, so moving the panel doesn't reconnect.
      if (this.users === 0) this.closeTimer = window.setTimeout(() => this.stop(), 3000);
    };
  }

  send(command: ShellCommand) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(command));
    else {
      this.queue.push(command);
      if (this.stopped) {
        this.stopped = false;
        void this.open();
      }
    }
  }

  private set(next: FolderShells) {
    this.state = next;
    for (const l of this.listeners) l();
  }

  private stop() {
    this.stopped = true;
    const ws = this.ws;
    this.ws = undefined;
    ws?.close();
  }

  private async open() {
    if (this.stopped) return;
    let url: string;
    try {
      const ep = await endpoint();
      url = `${ep.terminal}?${new URLSearchParams({ token: ep.token, watch: this.folder })}`;
    } catch {
      this.retry();
      return;
    }
    const ws = new WebSocket(url);
    this.ws = ws;
    ws.onopen = () => {
      this.backoff = 500;
      for (const command of this.queue.splice(0)) ws.send(JSON.stringify(command));
    };
    ws.onmessage = (e) => {
      if (typeof e.data !== "string") return;
      let msg: { type: string; shells?: Shell[]; active?: string; splits?: Record<string, string>; error?: string };
      try {
        msg = JSON.parse(e.data);
      } catch {
        return;
      }
      if (msg.type === "state") {
        this.set({ ready: true, shells: msg.shells ?? [], active: msg.active ?? "", splits: msg.splits ?? {} });
      } else if (msg.type === "error" && msg.error) {
        for (const l of errorListeners) l(this.folder, msg.error);
      }
    };
    ws.onclose = (e) => {
      if (this.ws !== ws) return;
      this.ws = undefined;
      // Never opened: am serve said no, most likely to a folder that's gone.
      if (!this.state.ready && e.code === 1006) {
        this.set({ ...EMPTY, ready: true, problem: "gone" });
      }
      this.retry();
    };
  }

  private retry() {
    if (this.stopped) return;
    const wait = this.backoff;
    this.backoff = Math.min(this.backoff * 2, 5000);
    window.setTimeout(() => void this.open(), wait);
  }
}

const watches = new Map<string, Watch>();
const errorListeners = new Set<(folder: string, error: string) => void>();

function watch(folder: string): Watch {
  let w = watches.get(folder);
  if (!w) {
    w = new Watch(folder);
    watches.set(folder, w);
  }
  return w;
}

/** The folder's shells, kept up to date while the caller is shown. */
export function useShells(folder: string): FolderShells {
  const w = watch(folder);
  useEffect(() => w.use(), [w]);
  return useSyncExternalStore((cb) => w.subscribe(cb), () => w.state);
}

/** What the folder's shells are now, without watching. */
export function shellsNow(folder: string): FolderShells {
  return watches.get(folder)?.state ?? EMPTY;
}

/** Asks am serve to change the folder's shells. */
export function shellCommand(folder: string, command: ShellCommand) {
  watch(folder).send(command);
}

/** What went wrong with a command, as am serve said it (an agent's terminal
 *  that can't be shown). */
export function onShellError(listener: (folder: string, error: string) => void): () => void {
  errorListeners.add(listener);
  return () => errorListeners.delete(listener);
}

/** The shell beside one, when its tab is split. */
export function splitPartner(state: FolderShells, id: string | undefined): Shell | undefined {
  const right = id ? state.splits[id] : undefined;
  return right ? state.shells.find((s) => s.id === right) : undefined;
}

export function isSplitHalf(state: FolderShells, id: string): boolean {
  return Object.values(state.splits).includes(id);
}
