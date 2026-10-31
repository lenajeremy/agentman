// Opening the workspace card on one of its lists from elsewhere: the review
// bar's Plans, the "N files changed" bar in a narrow thread
// (AgentThreadView.swift sets filesTab and shows the card).

import { useSyncExternalStore } from "react";

export type CardTab = "changes" | "files" | "history" | "plans";

const query = new URLSearchParams(location.search).get("files") as CardTab | null;
let state = { tab: (query ?? "changes") as CardTab, n: query ? 1 : 0 };
const listeners = new Set<() => void>();

/** Shows the card, opened up on a list. */
export function openCardTab(tab: CardTab) {
  state = { tab, n: state.n + 1 };
  for (const l of listeners) l();
}

export function useCardTab(): { tab: CardTab; n: number } {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    () => state,
  );
}
