// What the shell (appshell.go) tells the page and answers it: menu items for
// the page, the app coming to the front or leaving it, the screen locking,
// full screen, the quit check. In a plain browser the window's focus stands
// in for the app's, and the menu's shortcuts are the page's own.

import { Call, Events } from "@wailsio/runtime";
import { useSyncExternalStore } from "react";
import { unsavedFiles } from "./quitGuard";
import { hostCall } from "../host";

const inShell = location.protocol === "wails:" || location.hostname === "wails.localhost";

export type Command = "new-agent" | "toggle-sidebar" | "cycle-appearance" | "palette" | "agentman" | "away";

function on<T>(name: string, handler: (data: T) => void): () => void {
  if (!inShell) return () => {};
  return Events.On(name, (event) => handler(event.data as T));
}

/** A menu item meant for the page. (In the browser, with no menu bar,
 *  commands.ts takes the menu's shortcuts from the keyboard.) */
export function onCommand(handler: (command: Command) => void): () => void {
  return on<Command>("agentman:command", handler);
}

/** The screen locked or unlocked, or the computer is going to sleep. */
export function onAway(handler: (what: "lock" | "unlock" | "sleep") => void): () => void {
  return on("agentman:away", handler);
}

// Whether the app is in front, as NSApplication.isActive.
let active = inShell ? false : document.hasFocus();
const activeListeners = new Set<(active: boolean) => void>();

function setActive(next: boolean) {
  if (next === active) return;
  active = next;
  for (const l of activeListeners) l(next);
}

if (inShell) {
  // A test run is never in front, and says nothing; a real one answers.
  void Call.ByName("main.Shell.Active").then((a) => setActive(!!a)).catch(() => {});
  on<boolean>("agentman:active", setActive);
} else {
  window.addEventListener("focus", () => setActive(true));
  window.addEventListener("blur", () => setActive(false));
}

export function appActive(): boolean {
  return active;
}

export function onActive(handler: (active: boolean) => void): () => void {
  activeListeners.add(handler);
  return () => activeListeners.delete(handler);
}

export function useAppActive(): boolean {
  return useSyncExternalStore(
    (cb) => onActive(cb),
    () => active,
  );
}

// Full screen moves the window's buttons away, and the headers left.
let fullscreen = false;
const fullscreenListeners = new Set<() => void>();
on<boolean>("agentman:fullscreen", (value) => {
  fullscreen = value;
  for (const l of fullscreenListeners) l();
});

export function useFullscreen(): boolean {
  return useSyncExternalStore(
    (cb) => {
      fullscreenListeners.add(cb);
      return () => fullscreenListeners.delete(cb);
    },
    () => fullscreen,
  );
}

let home = "";
/** The user's home folder. */
export async function homeFolder(): Promise<string> {
  if (home) return home;
  home = String(await hostCall("Shell.Home").catch(() => ""));
  return home;
}

export function cachedHome(): string {
  return home;
}
void homeFolder();

// Quitting: the shell asks each window what it holds unsaved, then, after
// its alert, has them saved (AgentmanApp.swift's applicationShouldTerminate).
on<string>("agentman:quit-check", (token) => {
  const files = unsavedFiles().map(({ name, path }) => ({ name, path }));
  void Call.ByName("main.Shell.QuitAnswer", token, files);
});
on<string>("agentman:quit-save", async (token) => {
  let saved = true;
  for (const file of unsavedFiles()) {
    try {
      if (!(await file.save())) saved = false;
    } catch {
      saved = false;
    }
  }
  void Call.ByName("main.Shell.SaveAnswer", token, saved);
});

// In the browser, closing the tab with unsaved editor files asks first: the
// browser's own question, as a page can't word it.
if (!inShell) {
  window.addEventListener("beforeunload", (e) => {
    if (unsavedFiles().length === 0) return;
    e.preventDefault();
    e.returnValue = "";
  });
}
