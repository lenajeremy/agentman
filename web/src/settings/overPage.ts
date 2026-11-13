// Settings over the page, in a browser: a tab of its own there is out of
// the way, so openSettings shows it here, in the window the page is, and
// SettingsSheet draws it. The desktop shell keeps the Mac's window.

import { useSyncExternalStore } from "react";

/** The page asked for ("" for the last one shown), and which ask it was, so
 *  asking again, for pairing say, is seen even on the same page. */
export type SheetAsk = { page: string; n: number };

let asked: SheetAsk | null = null;
let asks = 0;
const listeners = new Set<() => void>();

function changed() {
  for (const listener of listeners) listener();
}

export function showSettingsSheet(page = "") {
  asked = { page, n: ++asks };
  changed();
}

export function closeSettingsSheet() {
  if (!asked) return;
  asked = null;
  changed();
}

export function useSettingsSheet(): SheetAsk | null {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => asked,
  );
}
