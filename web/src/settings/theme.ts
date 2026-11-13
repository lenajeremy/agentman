// Which look the app wears (Design/Brand.swift's AppTheme): today's,
// Classic, the default, or the website's, New, while it is refined. Kept as
// "appTheme", as the Mac keeps it, and applied as data-look="site" on the
// page, which styles/site.css draws from. Every window applies it as it
// opens and again when another window changes it.

import { useSyncExternalStore } from "react";
import { getPref, setPref } from "../prefs";
import "../styles/site.css";

export type ThemeChoice = "classic" | "site";

const KEY = "appTheme";
const listeners = new Set<() => void>();

/** The theme chosen. */
export function theme(): ThemeChoice {
  return getPref<string>(KEY, "classic") === "site" ? "site" : "classic";
}

/** The website's look is on (Look.site). */
export const siteLook = () => theme() === "site";

/** Changes the theme, in every window, and keeps it. */
export function setTheme(choice: ThemeChoice) {
  setPref(KEY, choice);
  apply();
}

/** The theme, kept current, for what draws differently in it. */
export function useTheme(): ThemeChoice {
  return useSyncExternalStore((cb) => {
    listeners.add(cb);
    return () => listeners.delete(cb);
  }, theme);
}

function apply() {
  const root = document.documentElement;
  if (theme() === "site") root.dataset.look = "site";
  else delete root.dataset.look;
  for (const listener of listeners) listener();
}

apply();

// Another window changed it.
window.addEventListener("storage", (e) => {
  if (e.key === `agentman.${KEY}`) apply();
});
