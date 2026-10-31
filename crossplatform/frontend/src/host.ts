// What the screens need from the shell around them, and nothing else, so the
// shell can change (Wails today) without the screens changing. In a plain
// browser, while the screens are developed, the Vite server stands in.

import { Daemon } from "../bindings/github.com/lenajeremy/agentman/crossplatform";

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
