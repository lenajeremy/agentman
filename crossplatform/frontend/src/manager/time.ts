// Times as the Mac app writes them (Date.formatted's hour and minute): in
// the user's region and with their 24-hour choice, which the shell reads
// from the system (locale.go), not the web view's language alone.

import { useSyncExternalStore } from "react";
import { Call } from "@wailsio/runtime";

const inShell = location.protocol === "wails:" || location.hostname === "wails.localhost";

let locale: string | undefined;
const listeners = new Set<() => void>();

if (inShell) {
  void (Call.ByName("main.Host.Locale") as Promise<string>)
    .then((tag) => {
      if (!tag) return;
      // A tag Intl doesn't know is left for its default.
      new Intl.DateTimeFormat(tag);
      locale = tag;
      for (const l of listeners) l();
    })
    .catch(() => {});
}

/** "15:32" or "3:32 PM", as the system's region has it. */
export function shortTime(ms: number): string {
  return new Date(ms).toLocaleTimeString(locale, { hour: "numeric", minute: "2-digit" });
}

/** Redraws once the region is known. */
export function useTimeLocale(): string | undefined {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    () => locale,
  );
}
