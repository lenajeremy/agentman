// What the screens need from the shell around them, and nothing else, so the
// shell can change (Wails today) without the screens changing. In a plain
// browser, while the screens are developed, the Vite server stands in.

import { Daemon, Host, Windows } from "../bindings/github.com/lenajeremy/agentman/crossplatform";
import { Events } from "@wailsio/runtime";
import { NotificationService } from "../bindings/github.com/wailsapp/wails/v3/pkg/services/notifications";

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

export interface EditorOptions {
  folder: string;
  sessionId?: string;
  file?: string;
  terminal?: boolean;
  agentHere?: boolean;
}

/** A folder in an editor window of its own, as the Mac app's editor scenes. */
export async function openEditor(opts: EditorOptions): Promise<void> {
  const full = { sessionId: "", file: "", terminal: false, agentHere: false, ...opts };
  if (inShell) return Windows.OpenEditor(full);
  const q = new URLSearchParams({ view: "editor", folder: full.folder });
  if (full.sessionId) q.set("session", full.sessionId);
  if (full.file) q.set("file", full.file);
  if (full.terminal) q.set("terminal", "1");
  if (full.agentHere) q.set("agent", "here");
  window.open(`/?${q}`, "_blank", "width=1200,height=800");
}

/** The Settings window, on a page ("" for the last one shown). */
export async function openSettings(page = ""): Promise<void> {
  if (inShell) return Windows.OpenSettings(page);
  window.open(`/?view=settings${page ? `&page=${page}` : ""}`, "_blank", "width=800,height=580");
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

/** A notification with the Mac's other parts: a subtitle, its thread
 *  (grouped in Notification Center), and no sound when silent. Asks for
 *  permission the first time, not at launch. Nothing where the shell can't
 *  post them (a development build on macOS). */
export interface Notice {
  id: string;
  title: string;
  subtitle?: string;
  body: string;
  link?: string;
  thread?: string;
  silent?: boolean;
}

let noticeAsked = false;

export async function notifyWith(n: Notice): Promise<void> {
  if (!inShell) return;
  try {
    if (!(await NotificationService.CheckNotificationAuthorization())) {
      if (noticeAsked) return;
      noticeAsked = true;
      if (!(await NotificationService.RequestNotificationAuthorization())) return;
    }
    await NotificationService.SendNotification({
      id: n.id, title: n.title, subtitle: n.subtitle, body: n.body, threadId: n.thread,
      data: { link: n.link ?? "" }, ...(n.silent ? { sound: { silent: true } } : {}),
    });
  } catch {
    // Not available here.
  }
}

/** Takes back every notification delivered, as opening Agentman does. */
export async function clearAllNotifications(): Promise<void> {
  if (inShell) await NotificationService.RemoveAllDeliveredNotifications().catch(() => {});
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
