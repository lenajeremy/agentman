import Constants from "expo-constants";
import { Platform, Settings } from "react-native";

/**
 * The route a simulator was started on with `-AGENTMAN_OPEN <route>`, for
 * screenshots and tests, where nothing can be tapped from outside; null on a
 * phone, or a simulator started as usual. Started this way the app asks for
 * nothing either: a permission prompt would cover the screen, and nothing
 * outside can answer it.
 */
export function simulatorRoute(): string | null {
  if (Platform.OS !== "ios" || Constants.isDevice) return null;
  const route = Settings.get("AGENTMAN_OPEN");
  return typeof route === "string" && route.startsWith("/") ? route : null;
}

/**
 * The appearance a simulator was started in with `-AGENTMAN_APPEARANCE
 * light|dark`, for screenshots of both themes; null otherwise. The app keeps
 * its own setting, so the simulator's appearance alone does not change it.
 */
export function simulatorAppearance(): "light" | "dark" | null {
  if (Platform.OS !== "ios" || Constants.isDevice) return null;
  const value = Settings.get("AGENTMAN_APPEARANCE");
  return value === "light" || value === "dark" ? value : null;
}

/**
 * How far down a settings page a simulator started with `-AGENTMAN_SCROLL
 * <points>` opens it, for screenshots of what lies below the fold; 0
 * otherwise.
 */
export function simulatorScroll(): number {
  if (Platform.OS !== "ios" || Constants.isDevice) return 0;
  const value = Number(Settings.get("AGENTMAN_SCROLL"));
  return Number.isFinite(value) && value > 0 ? value : 0;
}

/**
 * Whether a simulator started on a route was also started with
 * `-AGENTMAN_MENU session`, to show a session's "…" menu open: a menu is
 * opened by a tap, which nothing outside can make. Never on a phone.
 */
export function simulatorOpensSessionMenu(): boolean {
  return simulatorRoute() !== null && Settings.get("AGENTMAN_MENU") === "session";
}
