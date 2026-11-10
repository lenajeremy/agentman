// Light, dark, or as the computer is set: the "appearance" setting, as the
// Mac app's Appearance.apply (Views/SettingsView.swift). Every window
// applies it as it opens and again when another window changes it. It is
// applied twice over: natively (appearance.go), so title bars, menus and
// alerts follow, and as data-theme on the page (styles/appearance.css), so
// the page follows even where the system's preference can't be changed.

import { Call } from "@wailsio/runtime";
import { getPref, setPref } from "../prefs";
import "../styles/appearance.css";

export type AppearanceChoice = "system" | "light" | "dark";

const KEY = "appearance";
const inShell = location.protocol === "wails:" || location.hostname === "wails.localhost";

/** The appearance chosen: "system", "light" or "dark". */
export function appearance(): AppearanceChoice {
  const value = getPref<string>(KEY, "system");
  return value === "light" || value === "dark" ? value : "system";
}

/** Changes the appearance, in every window, and keeps it. */
export function setAppearance(choice: AppearanceChoice) {
  setPref(KEY, choice);
  apply(choice);
}

function apply(choice: AppearanceChoice) {
  const root = document.documentElement;
  if (choice === "system") delete root.dataset.theme;
  else root.dataset.theme = choice;
  if (inShell) void Call.ByName("main.Appearance.Set", choice).catch(() => {});
}

apply(appearance());

// Another window changed it.
window.addEventListener("storage", (e) => {
  if (e.key === `agentman.${KEY}`) apply(appearance());
});
