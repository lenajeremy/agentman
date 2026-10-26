/**
 * Which alerts this phone wants, and when the phone alerts at all.
 *
 * Two kinds from the agents, because they ask different things of you: "needs
 * you" means an agent is stopped until you answer, and "finished" means one is
 * done and waiting to be looked at. Someone who wants only the first should
 * not have to put up with the second.
 *
 * And who speaks. While Agentman is on, it hears every agent and tells you
 * what matters, so it is the one voice: the agents alert for themselves only
 * when you asked to hear from one, or from every agent.
 */

export interface NotifyPrefs {
  /** An agent finished its turn. */
  finished: boolean;
  /** An agent is blocked on an approval or a question. */
  needsYou: boolean;
  /** Agentman has something to say: a reply, a brief, word from an agent. */
  agentman: boolean;
  /** Each agent's own alerts even while Agentman is on and speaks for them. */
  agents: boolean;
}

/** One of the choices above. */
export type NotifyChoice = keyof NotifyPrefs;

/** What an alert is about, which is what the choices above choose by. */
export type AlertKind = "finished" | "needsYou" | "agentman";

/** Everything on, but the agents left to Agentman while it speaks for them. */
export const DEFAULT_NOTIFY_PREFS: NotifyPrefs = {
  finished: true,
  needsYou: true,
  agentman: true,
  agents: false,
};

export const NOTIFY_PREFS_KEY = "agentman.notifications.v1";

/** Reads stored preferences, falling back to the defaults for anything unreadable. */
export function parseNotifyPrefs(raw: string | null | undefined): NotifyPrefs {
  if (!raw) return { ...DEFAULT_NOTIFY_PREFS };
  try {
    const value = JSON.parse(raw) as Partial<Record<NotifyChoice, unknown>>;
    const read = (choice: NotifyChoice) =>
      typeof value[choice] === "boolean" ? (value[choice] as boolean) : DEFAULT_NOTIFY_PREFS[choice];
    return {
      finished: read("finished"),
      needsYou: read("needsYou"),
      agentman: read("agentman"),
      agents: read("agents"),
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

/**
 * Whether an agent alerts for itself, rather than through Agentman: always
 * while Agentman is off, and while it is on, only for an agent you asked to
 * hear from, or when you asked for every agent.
 */
export function agentSpeaks(prefs: NotifyPrefs, state: { agentmanOn: boolean; asked: boolean }): boolean {
  return !state.agentmanOn || state.asked || prefs.agents;
}
