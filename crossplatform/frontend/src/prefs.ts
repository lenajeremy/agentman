// What the Mac app keeps across launches in UserDefaults (@AppStorage):
// sidebar width, open panels, drafts, appearance… Here in the web view's
// storage, shared by every window of the app. Use the Mac app's own key
// names, so the two are easy to compare.

import { useCallback, useSyncExternalStore } from "react";

const PREFIX = "agentman.";
const listeners = new Map<string, Set<() => void>>();
const cache = new Map<string, unknown>();

function read<T>(key: string, fallback: T): T {
  if (cache.has(key)) return cache.get(key) as T;
  let value = fallback;
  try {
    const raw = localStorage.getItem(PREFIX + key);
    if (raw !== null) value = JSON.parse(raw) as T;
  } catch {
    // Storage off or the value unreadable: the default stands.
  }
  cache.set(key, value);
  return value;
}

export function getPref<T>(key: string, fallback: T): T {
  return read(key, fallback);
}

export function setPref<T>(key: string, value: T) {
  cache.set(key, value);
  try {
    localStorage.setItem(PREFIX + key, JSON.stringify(value));
  } catch {
    // Kept for this run only.
  }
  for (const l of listeners.get(key) ?? []) l();
}

/** A setting for this run of this window only, never written: what a test
 *  run starts with (AGENTMAN_OPEN's pref.<key>=<json>). */
export function overridePref<T>(key: string, value: T) {
  cache.set(key, value);
  for (const l of listeners.get(key) ?? []) l();
}

// A test run's settings, from the start, before anything reads them.
for (const [name, raw] of new URLSearchParams(location.search)) {
  if (!name.startsWith("pref.")) continue;
  try {
    cache.set(name.slice(5), JSON.parse(raw));
  } catch {
    cache.set(name.slice(5), raw);
  }
}

// Another window changed it.
window.addEventListener("storage", (e) => {
  if (!e.key?.startsWith(PREFIX)) return;
  const key = e.key.slice(PREFIX.length);
  cache.delete(key);
  for (const l of listeners.get(key) ?? []) l();
});

/** A kept setting, as @AppStorage(key) is: [value, set]. */
export function usePref<T>(key: string, fallback: T): [T, (value: T) => void] {
  const value = useSyncExternalStore(
    (cb) => {
      if (!listeners.has(key)) listeners.set(key, new Set());
      listeners.get(key)!.add(cb);
      return () => listeners.get(key)!.delete(cb);
    },
    () => read(key, fallback),
  );
  const set = useCallback((next: T) => setPref(key, next), [key]);
  return [value, set];
}
