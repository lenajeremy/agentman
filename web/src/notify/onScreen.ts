// What the editors' agent panels show (AgentsStore.swift's showInPanel and
// hideFromPanel): an agent's thread, or Agentman ("agentman"). What a panel
// shows counts as looked at, as the main window's thread does: no
// notification for it, and the daemon is told. Each window is a page of its
// own, so the panels say it over a channel every window hears.
//
//   const done = showInPanel(sessionId);   // when a panel shows it
//   done();                                // when it stops

const channel = typeof BroadcastChannel === "undefined" ? null : new BroadcastChannel("agentman-panels");
const windowId = crypto.randomUUID();
/** Each window's panels: window → what its panels show, counted. */
const shown = new Map<string, Map<string, number>>();
const mine = new Map<string, number>();
const listeners = new Set<() => void>();

function changed() {
  for (const l of listeners) l();
}

function announce() {
  channel?.postMessage({ window: windowId, shown: [...mine] });
}

channel?.addEventListener("message", (e: MessageEvent) => {
  const data = e.data as { window?: string; shown?: [string, number][]; ask?: boolean; gone?: string };
  if (data.ask) {
    announce();
    return;
  }
  if (data.gone) {
    shown.delete(data.gone);
    changed();
    return;
  }
  if (data.window && data.shown) {
    shown.set(data.window, new Map(data.shown));
    changed();
  }
});
// A window that opens late learns what the others show.
channel?.postMessage({ ask: true });
window.addEventListener("pagehide", () => channel?.postMessage({ gone: windowId }));

/** A panel shows an agent's thread, or Agentman with "agentman". */
export function showInPanel(key: string): () => void {
  mine.set(key, (mine.get(key) ?? 0) + 1);
  announce();
  changed();
  let done = false;
  return () => {
    if (done) return;
    done = true;
    const n = (mine.get(key) ?? 1) - 1;
    if (n > 0) mine.set(key, n);
    else mine.delete(key);
    announce();
    changed();
  };
}

/** Everything every window's panels show. */
export function panelsShowing(): string[] {
  const all = new Set(mine.keys());
  for (const panels of shown.values()) for (const key of panels.keys()) all.add(key);
  return [...all];
}

export function onPanelsChange(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
