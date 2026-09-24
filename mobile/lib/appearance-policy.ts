/**
 * Which palette the app draws with.
 *
 * Light is the default whatever the phone is set to: it is the look the app is
 * designed around. Dark and "follow the phone" are opt-in from Settings.
 */
export type AppearancePreference = "light" | "dark" | "system";
export type Scheme = "light" | "dark";

export const DEFAULT_APPEARANCE: AppearancePreference = "light";
export const APPEARANCE_STORAGE_KEY = "agentman.appearance.v1";

/** Anything unreadable falls back to the default rather than guessing. */
export function parseAppearance(raw: string | null | undefined): AppearancePreference {
  return raw === "light" || raw === "dark" || raw === "system" ? raw : DEFAULT_APPEARANCE;
}

/** The palette to draw with, given the preference and what the phone reports. */
export function resolveScheme(
  preference: AppearancePreference,
  system: string | null | undefined,
): Scheme {
  if (preference !== "system") return preference;
  return system === "dark" ? "dark" : "light";
}

/**
 * What to tell the OS, so alerts, the keyboard and other native chrome match
 * the app. "unspecified" hands the decision back to the phone's own setting.
 */
export function nativeScheme(preference: AppearancePreference): "light" | "dark" | "unspecified" {
  return preference === "system" ? "unspecified" : preference;
}
