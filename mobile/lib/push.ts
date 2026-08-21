/**
 * Remote push registration.
 *
 * Local notifications are scheduled by this app when a websocket event arrives,
 * which only works while the socket is alive. iOS suspends a backgrounded app
 * and the socket dies with it — precisely when the user has walked away and
 * most wants to hear that their agent finished. A push token lets the Mac
 * reach the phone through APNs instead, with no socket involved.
 *
 * The token goes only to the paired Mac, which posts directly to Expo. The
 * relay never sees it.
 */
import Constants from "expo-constants";
import * as Notifications from "expo-notifications";
import { Platform } from "react-native";

import { looksLikePushToken } from "./push-token";

export { looksLikePushToken };

/**
 * True once the Mac has accepted a push token, meaning alerts now arrive from
 * the daemon. The app stops scheduling its own from that point, so one finished
 * turn does not produce two notifications.
 */
let pushActive = false;

export function isPushActive(): boolean {
  return pushActive;
}

export function setPushActive(active: boolean): void {
  pushActive = active;
}

/**
 * The EAS project the token is minted against.
 *
 * Read from both places it can live. expoConfig is populated from the manifest
 * and is reliably there in development, but a production build may have none —
 * in which case the id is only on easConfig. Reading just the first returns
 * undefined on exactly the builds where push is supposed to work.
 */
function projectId(): string | undefined {
  const extra = Constants.expoConfig?.extra as
    | { eas?: { projectId?: string } }
    | undefined;
  return extra?.eas?.projectId ?? Constants.easConfig?.projectId ?? undefined;
}

/**
 * Why the last attempt produced no token.
 *
 * Every failure here is expected somewhere — a simulator, Expo Go, a refusal —
 * so none of them throw. That silence cost a build cycle of guessing, so the
 * reason is now kept and surfaced rather than swallowed.
 */
let lastFailure = "";

export function pushFailureReason(): string {
  return lastFailure;
}

/**
 * Requests permission and returns an Expo push token.
 *
 * Returns null rather than throwing on every expected failure — no permission,
 * a simulator, Expo Go without a project, web. Push is an enhancement, and the
 * app must keep working on local notifications when it is unavailable.
 */
export async function obtainPushToken(): Promise<string | null> {
  lastFailure = "";
  if (Platform.OS === "web") {
    lastFailure = "Not supported on web.";
    return null;
  }

  try {
    const existing = await Notifications.getPermissionsAsync();
    let granted = existing.granted;
    if (!granted && existing.canAskAgain) {
      granted = (await Notifications.requestPermissionsAsync()).granted;
    }
    if (!granted) {
      lastFailure = "Notifications are turned off for this app in Settings.";
      return null;
    }

    const id = projectId();
    if (!id) {
      lastFailure = "This build carries no EAS project id.";
      return null;
    }

    const token = await Notifications.getExpoPushTokenAsync({ projectId: id });
    if (!looksLikePushToken(token.data)) {
      lastFailure = `Apple returned an unexpected token: ${String(token.data).slice(0, 40)}`;
      return null;
    }
    return token.data;
  } catch (error) {
    // A simulator has no APNs registration and Expo Go has no entitlement, so
    // this path is reached legitimately. Recording why is what makes a real
    // failure on a real device diagnosable instead of invisible.
    lastFailure = error instanceof Error ? error.message : String(error);
    return null;
  }
}
