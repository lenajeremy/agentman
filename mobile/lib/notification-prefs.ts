/**
 * Which alerts this phone wants, and when the phone alerts at all.
 *
 * Two kinds, because they ask different things of you: "needs you" means an
 * agent is stopped until you answer, and "finished" means one is done and
 * waiting to be looked at. Someone who wants only the first should not have
 * to put up with the second.
 */

export interface NotifyPrefs {
  /** An agent finished its turn. */
  finished: boolean;
  /** An agent is blocked on an approval or a question. */
  needsYou: boolean;
}

export type AlertKind = keyof NotifyPrefs;

/** Everything on: what every phone received before there was a choice. */
export const DEFAULT_NOTIFY_PREFS: NotifyPrefs = { finished: true, needsYou: true };

export const NOTIFY_PREFS_KEY = "agentman.notifications.v1";

/** Reads stored preferences, falling back to the defaults for anything unreadable. */
export function parseNotifyPrefs(raw: string | null | undefined): NotifyPrefs {
  if (!raw) return { ...DEFAULT_NOTIFY_PREFS };
  try {
    const value = JSON.parse(raw) as Partial<Record<AlertKind, unknown>>;
    return {
      finished: typeof value.finished === "boolean" ? value.finished : DEFAULT_NOTIFY_PREFS.finished,
      needsYou: typeof value.needsYou === "boolean" ? value.needsYou : DEFAULT_NOTIFY_PREFS.needsYou,
    };
  } catch {
    return { ...DEFAULT_NOTIFY_PREFS };
  }
}

/**
 * Whether the app should raise an alert of its own.
 *
 * Never while the app is open: you are already looking, and a banner over the
 * screen you are reading is the noise this exists to cut. Never once the Mac
 * sends alerts by push either, or every alert would arrive twice. Otherwise,
 * only the kinds you kept.
 */
export function shouldAlertOnPhone(
  kind: AlertKind,
  prefs: NotifyPrefs,
  state: { appActive: boolean; pushActive: boolean },
): boolean {
  if (state.appActive || state.pushActive) return false;
  return prefs[kind];
}
