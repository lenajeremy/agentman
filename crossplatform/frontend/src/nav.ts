// Where the main window is, and the agentman:// links that move it
// (AgentsStore.swift's open(url:), ContentView.swift's onOpenURL). The detail
// area shows Agentman (the default), New Agent, or an agent's thread. Links
// to the editor or settings open their own windows.

import { useSyncExternalStore } from "react";
import { onOpenLink, openEditor, openSettings } from "./host";

export type Route =
  | { kind: "agentman" }
  | { kind: "new"; worktree?: boolean }
  | { kind: "agent"; id: string };

const query = new URLSearchParams(location.search);
let route: Route = query.get("agent") ? { kind: "agent", id: query.get("agent")! } : { kind: "agentman" };
const listeners = new Set<() => void>();

export function go(next: Route) {
  route = next;
  for (const l of listeners) l();
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
export const openNewAgent = (worktree = false) => go({ kind: "new", worktree });

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
    case "new":
      openNewAgent();
      break;
    case "home":
      openAgentman();
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
    case "settings":
      void openSettings(parts[1] ?? "");
      break;
  }
}

onOpenLink(openLink);
