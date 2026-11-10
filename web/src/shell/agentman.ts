// What the shell needs to know of Agentman beside the Agentman area's own
// screens: whether it is on (its key set, ManagerStore.on), for the row
// menu's "Notify Me About This Agent", the palette's "Ask Agentman" and the
// away summary's "Ask Agentman for more"; and a way to ask it something.

import { useSyncExternalStore } from "react";
import type { ManagerSettings, ManagerStatus } from "@shared/protocol";
import { daemon } from "../daemon";
import { openAgentmanChat } from "../nav";

let keySet = false;
let fromSettings = false;
const listeners = new Set<() => void>();

function setKeySet(next: boolean) {
  if (next === keySet) return;
  keySet = next;
  for (const l of listeners) l();
}

daemon.on((event) => {
  if (event.type === "manager_settings") {
    const settings = event.managerSettings as ManagerSettings | undefined;
    if (settings) {
      fromSettings = true;
      setKeySet(settings.keySet);
    }
  } else if (event.type === "manager_status" && !fromSettings) {
    const status = event.managerStatus as ManagerStatus | undefined;
    if (status) setKeySet(status.keySet);
  }
});

daemon.onStatus((status) => {
  if (status !== "connected") return;
  fromSettings = false;
  daemon.send({ type: "manager_settings" });
});

/** Agentman is on: its engine has a key. */
export function useAgentmanOn(): boolean {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    () => keySet,
  );
}

export function agentmanOn(): boolean {
  return keySet;
}

/** Opens Agentman and asks it something. */
export function askAgentman(text: string) {
  openAgentmanChat();
  void daemon.request({ type: "manager_send", text, clientId: crypto.randomUUID() }).catch(() => {});
}
