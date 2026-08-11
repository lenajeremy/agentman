import AsyncStorage from "@react-native-async-storage/async-storage";

import type { Dismissals } from "./dismissRules";

/**
 * Sessions you have swiped away, kept on the device.
 *
 * A session cannot actually be deleted: it exists because an agent is running
 * on your machine, and discovery finds it again a second later. So this hides
 * it instead, and the interesting part is when it comes back — see
 * ./dismissRules, which holds that logic and is tested directly.
 *
 * Device-local, for the same reason as drafts: which sessions you want to see
 * is a view preference, not agent state. Putting it on the daemon would mean
 * swiping on your phone silently changes what `am list` prints in your
 * terminal, and the terminal should show what is actually running.
 */
const KEY = "agentman.dismissed";

export * from "./dismissRules";

export async function loadDismissals(): Promise<Dismissals> {
  try {
    const raw = await AsyncStorage.getItem(KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as unknown;
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    // Hand-editable storage and older shapes both end up here, and a bad entry
    // must not hide a live session forever.
    const clean: Dismissals = {};
    for (const [id, value] of Object.entries(parsed as Record<string, unknown>)) {
      const entry = value as { activityAt?: unknown; at?: unknown };
      if (typeof entry?.activityAt === "number" && typeof entry?.at === "number") {
        clean[id] = { activityAt: entry.activityAt, at: entry.at };
      }
    }
    return clean;
  } catch {
    return {};
  }
}

export async function saveDismissals(dismissals: Dismissals): Promise<void> {
  try {
    await AsyncStorage.setItem(KEY, JSON.stringify(dismissals));
  } catch {
    // Losing a dismissal means a row you hid comes back, which is annoying but
    // not worth an error dialog over.
  }
}
