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
