// What the screens need from the shell around them, and nothing else, so the
// shell can change (Wails today) without the screens changing. In a plain
// browser, while the screens are developed, the Vite server stands in.

import { Daemon, Host } from "../bindings/github.com/lenajeremy/agentman/crossplatform";
import { Events } from "@wailsio/runtime";

export interface Endpoint {
  url: string;
  terminal: string;
  token: string;
}

export type Platform = "darwin" | "linux" | "windows" | "browser";

/** Running inside the Wails shell, rather than in a browser: its pages come
 *  from wails:// on macOS and Linux, and http://wails.localhost on Windows. */
const inShell = location.protocol === "wails:" || location.hostname === "wails.localhost";

/** Where am serve is, and the token it gave out at its last start. */
export async function endpoint(): Promise<Endpoint> {
  if (inShell) return (await Daemon.Endpoint()) as Endpoint;
  const res = await fetch("/__agentman/endpoint");
  if (!res.ok) throw new Error("am serve isn't running");
  return res.json();
}

let platformCache: Platform | undefined;

export async function platform(): Promise<Platform> {
  if (platformCache) return platformCache;
  platformCache = inShell ? ((await Daemon.Platform()) as Platform) : "browser";
  document.documentElement.dataset.platform = platformCache;
  return platformCache;
}

/** An agent's folder in an editor window of its own. */
export async function openEditor(sessionId: string, file = ""): Promise<void> {
  if (inShell) return Daemon.OpenEditor(sessionId, file);
  const q = new URLSearchParams({ view: "editor", session: sessionId, ...(file ? { file } : {}) });
  window.open(`/?${q}`, "_blank", "width=1200,height=800");
}

// What the system does for the screens (host.go). In a browser, while the
// screens are developed, each does the nearest thing a page can, or nothing.

/** A folder from the system's picker; "" when cancelled. */
export async function pickFolder(title: string, start = ""): Promise<string> {
  return inShell ? Host.PickFolder(title, start) : (prompt(title) ?? "");
}

/** Files from the system's picker; none when cancelled. */
export async function pickFiles(title: string, start = ""): Promise<string[]> {
  return inShell ? Host.PickFiles(title, start) : [];
}

/** Shows a file or folder in Finder, or the Linux file manager. */
export async function reveal(path: string): Promise<void> {
  if (inShell) await Host.Reveal(path);
}

/** Opens a file in the app the system has for it. */
export async function openPath(path: string): Promise<void> {
  if (inShell) await Host.OpenPath(path);
}

/** Opens a web link in the browser. */
export async function openURL(url: string): Promise<void> {
  if (inShell) await Host.OpenURL(url);
  else window.open(url, "_blank", "noopener");
}

/** A system notification; clicking it opens `link` (an agentman:// route). */
export async function notify(id: string, title: string, body: string, link = ""): Promise<void> {
  if (inShell) await Host.Notify(id, title, body, link);
}

export async function clearNotification(id: string): Promise<void> {
  if (inShell) await Host.ClearNotification(id);
}

/** The dock icon's badge ("" clears it). macOS only. */
export async function setBadge(label: string): Promise<void> {
  if (inShell) await Host.SetBadge(label);
}

/** Links to follow: a clicked notification's, and later agentman:// links. */
export function onOpenLink(handler: (link: string) => void): () => void {
  if (!inShell) return () => {};
  return Events.On("agentman:open", (event) => handler(String(event.data)));
}
