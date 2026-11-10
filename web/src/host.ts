// What the screens need from the shell around them, and nothing else, so the
// shell can change (Wails today) without the screens changing. In a plain
// browser, while the screens are developed, the Vite server stands in.

import { openPanel } from "./picker/OpenPanel";
import { Daemon, Host, Windows } from "../bindings/github.com/lenajeremy/agentman/crossplatform";
import { Call, Events } from "@wailsio/runtime";
import { NotificationService } from "../bindings/github.com/wailsapp/wails/v3/pkg/services/notifications";

export interface Endpoint {
  url: string;
  terminal: string;
  token: string;
}

export type Platform = "darwin" | "linux" | "windows" | "browser";

/** Running inside the Wails shell, rather than in a browser: its pages come
 *  from wails:// on macOS and Linux, and http://wails.localhost on Windows. */
export const inShell = location.protocol === "wails:" || location.hostname === "wails.localhost";

/**
 * Asks the computer for what a page can't do itself (internal/apphost):
 * "Files.Folder", "Files.Read", "Shell.Home"… In the desktop shell its
 * services answer; in the browser am serve does, at /app/host/<name>,
 * signed in by the session cookie.
 */
export async function hostCall<T>(name: string, ...args: unknown[]): Promise<T> {
  if (inShell) return Call.ByName(`main.${name}`, ...args) as Promise<T>;
  const res = await fetch(`/app/host/${name}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(args),
  });
  const body = (await res.json().catch(() => ({ error: res.statusText }))) as { result?: T; error?: string };
  if (!res.ok || body.error) throw new Error(body.error || res.statusText);
  return body.result as T;
}

/** Where am serve is, and the token it gave out at its last start. */
export async function endpoint(): Promise<Endpoint> {
  if (inShell) return (await Daemon.Endpoint()) as Endpoint;
  // On Vite's development server, which hands over a test daemon's endpoint.
  if (import.meta.env.DEV) {
    const res = await fetch("/__agentman/endpoint");
    if (!res.ok) throw new Error("am serve isn't running");
    return res.json();
  }
  // Served by am serve (am web): the same address, signed in by the
  // session cookie the browser already holds, so no token.
  const scheme = location.protocol === "https:" ? "wss:" : "ws:";
  return { url: `${scheme}//${location.host}/app`, terminal: `${scheme}//${location.host}/app/terminal`, token: "" };
}

let platformCache: Platform | undefined;

export async function platform(): Promise<Platform> {
  if (platformCache) return platformCache;
  platformCache = inShell ? ((await Daemon.Platform()) as Platform) : "browser";
  document.documentElement.dataset.platform = platformCache;
  return platformCache;
}

let systemCache: Promise<string> | null = null;
let systemKnown: string | null = null;

/** The system am serve runs on: "darwin", "linux" or "windows"; "" until
 *  known. In the shell, the computer's own; in a browser, which can't tell
 *  from itself, am serve says (Host.System). */
export function serverSystem(): Promise<string> {
  systemCache ??= (inShell ? platform().then(String) : hostCall<string>("Host.System").then(String))
    .catch(() => "")
    .then((system) => (systemKnown = system));
  return systemCache;
}

/** The system, once asked: null until am serve answers, "" if it couldn't. */
export const knownSystem = () => systemKnown;

/** Where am serve keeps an API key (internal/secrets): the Mac's Keychain,
 *  or elsewhere a file only the user can read. */
export function keptWhere(system: string): string {
  if (system === "darwin") return "kept in your Mac's Keychain";
  if (system) return "kept in a file only you can read";
  return "kept on this computer";
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
  // This page is that folder's editor: it opens the file itself, rather than
  // another window or a reload of this one (EditorWindow.tsx).
  const here = new URLSearchParams(location.search);
  const trim = (path: string) => path.replace(/\/+$/, "");
  if (here.get("view") === "editor" && trim(here.get("folder") ?? "") === trim(full.folder)) {
    window.dispatchEvent(new CustomEvent("agentman:editor-open", { detail: full }));
    return;
  }
  if (inShell) return Windows.OpenEditor(full);
  const q = new URLSearchParams({ view: "editor", folder: full.folder });
  if (full.sessionId) q.set("session", full.sessionId);
  if (full.file) q.set("file", full.file);
  if (full.terminal) q.set("terminal", "1");
  if (full.agentHere) q.set("agent", "here");
  // One window per folder, as the Mac's: opening it again brings it back.
  window.open(`/?${q}`, `agentman-editor:${full.folder}`, "width=1200,height=800");
}

/** The Settings window, on a page ("" for the last one shown). */
export async function openSettings(page = ""): Promise<void> {
  if (inShell) return Windows.OpenSettings(page);
  // One Settings window, as the Mac's: opening it again shows the page asked
  // for in the same window.
  window.open(`/?view=settings${page ? `&page=${page}` : ""}`, "agentman-settings", "width=800,height=580");
}

// What the system does for the screens (host.go). In a browser, while the
// screens are developed, each does the nearest thing a page can, or nothing.

/** A folder from the system's picker; "" when cancelled. */
export async function pickFolder(title: string, start = "", opts: { button?: string; newFolder?: boolean } = {}): Promise<string> {
  if (inShell) return Host.PickFolder(title, start);
  const [folder] = await openPanel({ folders: true, message: title, start, button: opts.button ?? "Open", newFolder: opts.newFolder });
  return folder ?? "";
}

/** Files from the system's picker; none when cancelled. In the browser,
 *  the page's own panel (picker/OpenPanel.tsx), which answers with paths. */
export async function pickFiles(title: string, start = "", opts: { images?: boolean } = {}): Promise<string[]> {
  if (inShell) return Host.PickFiles(title, start);
  return openPanel({ multiple: true, message: title, start, images: opts.images });
}

/** Shows a file or folder in Finder, or the Linux file manager. */
export async function reveal(path: string): Promise<void> {
  if (inShell) await Host.Reveal(path);
  else await hostCall("Host.Reveal", path);
}

/** Opens a file in the app the system has for it. In the browser am serve
 *  does both, on the same computer. */
export async function openPath(path: string): Promise<void> {
  if (inShell) await Host.OpenPath(path);
  else await hostCall("Host.OpenPath", path);
}

/** Opens a web link in the browser. */
export async function openURL(url: string): Promise<void> {
  if (inShell) await Host.OpenURL(url);
  else window.open(url, "_blank", "noopener");
}

/** A system notification; clicking it opens `link` (an agentman:// route).
 *  In the browser, the browser's own: permission is asked the first time
 *  there is something to say, and a click brings the tab forward. */
export async function notify(id: string, title: string, body: string, link = ""): Promise<void> {
  if (inShell) return Host.Notify(id, title, body, link);
  if (typeof Notification === "undefined") return;
  if (Notification.permission === "default") await Notification.requestPermission();
  if (Notification.permission !== "granted") return;
  shown.get(id)?.close();
  const note = new Notification(title, { body, tag: id, icon: "/assets/agentman.png" });
  shown.set(id, note);
  note.onclick = () => {
    window.focus();
    note.close();
    if (link) for (const handler of linkHandlers) handler(link);
  };
  note.onclose = () => shown.delete(id);
}

const shown = new Map<string, Notification>();

export async function clearNotification(id: string): Promise<void> {
  if (inShell) return Host.ClearNotification(id);
  shown.get(id)?.close();
}

/** A notification with the Mac's other parts: a subtitle, its thread
 *  (grouped in Notification Center), and no sound when silent. Asks for
 *  permission the first time, not at launch. Nothing where the shell can't
 *  post them (a development build on macOS). In the browser, the browser's
 *  own notification, the subtitle above the body. */
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
  if (!inShell) {
    const body = n.subtitle ? `${n.subtitle}\n${n.body}` : n.body;
    return notify(n.id, n.title, body, n.link ?? "");
  }
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
  if (inShell) return NotificationService.RemoveAllDeliveredNotifications().catch(() => {});
  for (const note of shown.values()) note.close();
}

/** The dock icon's badge ("" clears it), macOS only; in the browser the
 *  tab's title carries the count, "(3) Agentman". */
export async function setBadge(label: string): Promise<void> {
  if (inShell) return Host.SetBadge(label);
  const title = document.title.replace(/^\(\S+\) /, "");
  document.title = label ? `(${label}) ${title}` : title;
}

const linkHandlers = new Set<(link: string) => void>();

/** Links to follow: a clicked notification's, and later agentman:// links;
 *  in the browser also those an editor tab hands over (openInMain). */
export function onOpenLink(handler: (link: string) => void): () => void {
  if (inShell) return Events.On("agentman:open", (event) => handler(String(event.data)));
  linkHandlers.add(handler);
  // Only the main window takes them: an editor or Settings tab (?view=) is
  // where they come from.
  const main = !new URLSearchParams(location.search).has("view");
  const channel = !main || typeof BroadcastChannel === "undefined" ? null : new BroadcastChannel(LINKS);
  if (channel) channel.onmessage = (e) => typeof e.data === "string" && handler(e.data);
  const stopSaying = main ? sayMainIsHere() : () => {};
  return () => {
    linkHandlers.delete(handler);
    channel?.close();
    stopSaying();
  };
}

const LINKS = "agentman-links";

// The other tabs know whether a main window is open: each says it is, on
// opening, when asked, every 20 s, and that it's gone when it goes. A link
// for the main window where none is open opens one; that has to happen in
// the click itself, as a browser won't open a window a moment later.
const PRESENCE = "agentman-main";
const HEARD_FOR = 90_000; // a hidden tab's timers can slow to once a minute
const mainsHeard = new Map<string, number>();

function sayMainIsHere(): () => void {
  if (typeof BroadcastChannel === "undefined") return () => {};
  const id = crypto.randomUUID();
  const channel = new BroadcastChannel(PRESENCE);
  const say = () => channel.postMessage({ main: id });
  channel.onmessage = (e) => e.data?.ask && say();
  say();
  const timer = window.setInterval(say, 20_000);
  const gone = () => channel.postMessage({ gone: id });
  window.addEventListener("pagehide", gone);
  return () => {
    window.clearInterval(timer);
    window.removeEventListener("pagehide", gone);
    gone();
    channel.close();
  };
}

if (!inShell && new URLSearchParams(location.search).has("view") && typeof BroadcastChannel !== "undefined") {
  const channel = new BroadcastChannel(PRESENCE);
  channel.onmessage = (e) => {
    if (typeof e.data?.main === "string") mainsHeard.set(e.data.main, Date.now());
    if (typeof e.data?.gone === "string") mainsHeard.delete(e.data.gone);
  };
  channel.postMessage({ ask: true });
}

/** Whether a main window was heard from lately. */
function mainIsOpen(): boolean {
  const now = Date.now();
  return [...mainsHeard.values()].some((at) => now - at < HEARD_FOR);
}

/** An agentman:// link for the main window, from an editor window: in the
 *  browser, another tab, reached over a BroadcastChannel and brought
 *  forward where the browser lets it. */
export function openInMain(link: string) {
  if (inShell) {
    void Events.Emit("agentman:open", link);
    return;
  }
  if (typeof BroadcastChannel === "undefined" || !mainIsOpen()) {
    window.open(`/?open=${encodeURIComponent(link)}`, "_blank");
    return;
  }
  const channel = new BroadcastChannel(LINKS);
  channel.postMessage(link);
  channel.close();
  try {
    window.opener?.focus();
  } catch {
    // Another page's, or none.
  }
}
