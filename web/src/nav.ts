// Where the main window is, and the agentman:// links that move it
// (AgentsStore.swift's open(url:), ContentView.swift's onOpenURL). The detail
// area shows Agentman (the default), New Agent, or an agent's thread. Links
// to the editor or settings open their own windows.

import { setBoardChatOpen } from "./manager/store";
import { useSyncExternalStore } from "react";
import { onOpenLink, openEditor, openSettings } from "./host";
import { showLastAway } from "./away/away";
import { setShell } from "./shell/state";

export type Route =
  | { kind: "agentman" }
  | { kind: "new"; worktree?: boolean }
  | { kind: "agent"; id: string }
  | { kind: "usage" };

const query = new URLSearchParams(location.search);
let route: Route = routeOf(query);
const listeners = new Set<() => void>();

/** The page an address names: ?agent=<id>, ?page=new|new-worktree|usage,
 *  or the Board. */
export function routeOf(params: URLSearchParams): Route {
  const agent = params.get("agent");
  if (agent) return { kind: "agent", id: agent };
  switch (params.get("page")) {
    case "new": return { kind: "new" };
    case "new-worktree": return { kind: "new", worktree: true };
    case "usage": return { kind: "usage" };
    default: return { kind: "agentman" };
  }
}

/** The address of a page, as routeOf reads it. */
export function addressOf(r: Route): string {
  switch (r.kind) {
    case "agent": return `?agent=${encodeURIComponent(r.id)}`;
    case "new": return r.worktree ? "?page=new-worktree" : "?page=new";
    case "usage": return "?page=usage";
    default: return "";
  }
}

// Launch options a page is opened with once (shell/commands.ts), not a
// page to go Back to.
const LAUNCH = ["open", "palette", "folder", "away", "lightbox", "renaming", "next"];

/** In a browser the address follows the main window's page, so reloading
 *  or going Back finds it again; the Mac has no address to keep. */
function follow(r: Route) {
  if (new URLSearchParams(location.search).has("view")) return;
  const address = addressOf(r);
  if (location.search === address) return;
  const once = LAUNCH.some((name) => new URLSearchParams(location.search).has(name));
  const url = address || location.pathname;
  if (once) history.replaceState(null, "", url);
  else history.pushState(null, "", url);
}

function same(a: Route, b: Route): boolean {
  return a.kind === b.kind && (a.kind !== "agent" || (b.kind === "agent" && a.id === b.id))
    && (a.kind !== "new" || (b.kind === "new" && !!a.worktree === !!b.worktree));
}

export function go(next: Route) {
  if (same(next, route)) return;
  route = next;
  follow(next);
  for (const l of listeners) l();
}

// Back and Forward go to the page the address names.
window.addEventListener("popstate", () => {
  const next = routeOf(new URLSearchParams(location.search));
  if (same(next, route)) return;
  route = next;
  for (const l of listeners) l();
});

/** Where the main window is now, for code outside React. */
export function getRoute(): Route {
  return route;
}

/** Told each time the main window goes somewhere else. */
export function onRouteChange(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function useRoute(): Route {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    () => route,
  );
}

export const openAgent = (id: string) => go({ kind: "agent", id });
export const openAgentman = () => go({ kind: "agentman" });

/** The Board, with the conversation with Agentman open beside it
 *  (AgentsStore.openAgentmanChat): for a toast, agentman://home, and asking
 *  Agentman from elsewhere. */
export function openAgentmanChat() {
  go({ kind: "agentman" });
  setBoardChatOpen(true);
}
export const openNewAgent = (worktree = false) => go({ kind: "new", worktree });
/** The usage page: what the agents spent, and what is left of each plan. */
export const openUsage = () => go({ kind: "usage" });

/** Follows an agentman:// link; any other scheme is ignored. */
export function openLink(link: string) {
  let url: URL;
  try {
    url = new URL(link);
  } catch {
    return;
  }
  if (url.protocol !== "agentman:" && url.protocol !== "agentman-debug:") return;
  // agentman://agent/<id>: the host is the first part, the rest the path.
  const parts = [url.hostname, ...url.pathname.split("/").filter(Boolean)].map(decodeURIComponent);
  switch (parts[0]) {
    case "agent":
      if (parts[1]) openAgent(parts.slice(1).join("/"));
      break;
    // agentman://found/<session>?message=<id>&q=<words>: a message found
    // from an editor's ⌘K, opened at its place.
    case "found": {
      const id = parts.slice(1).join("/");
      if (!id) break;
      // Loaded when needed: the search imports this module.
      void import("./board/search").then(({ openFound }) => openFound(id, url.searchParams.get("message"), url.searchParams.get("q") ?? ""));
      break;
    }
    case "new":
      openNewAgent();
      break;
    case "home":
      openAgentmanChat();
      break;
    case "usage":
      openUsage();
      break;
    case "away":
      showLastAway();
      break;
    // agentman://filter/<state>, or /all: the sidebar's filter.
    case "filter":
      if (parts[1] === "all") setShell({ stateFilter: null, folderFilter: null });
      else if (parts[1] === "waiting_input" || parts[1] === "busy" || parts[1] === "idle") setShell({ stateFilter: parts[1] });
      break;
    case "terminal": {
      const folder = url.searchParams.get("folder");
      if (folder) void openEditor({ folder, terminal: true });
      break;
    }
    case "edit": {
      const folder = url.searchParams.get("folder");
      if (folder) void openEditor({ folder, file: url.searchParams.get("file") ?? "", agentHere: url.searchParams.get("agent") === "here" });
      break;
    }
    case "settings": {
      // A page it has, else the last one shown.
      const pages = ["general", "notifications", "devices", "pair", "permissions", "personality", "knowledge", "keys", "languages"];
      void openSettings(pages.includes(parts[1] ?? "") ? parts[1] : "");
      break;
    }
  }
}

onOpenLink(openLink);
