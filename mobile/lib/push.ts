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

/** The EAS project the token is minted against. */
function projectId(): string | undefined {
  const extra = Constants.expoConfig?.extra as
    | { eas?: { projectId?: string } }
    | undefined;
  return extra?.eas?.projectId ?? undefined;
}

/**
 * Requests permission and returns an Expo push token.
 *
 * Returns null rather than throwing on every expected failure — no permission,
 * a simulator, Expo Go without a project, web. Push is an enhancement, and the
 * app must keep working on local notifications when it is unavailable.
 */
export async function obtainPushToken(): Promise<string | null> {
  if (Platform.OS === "web") return null;

  try {
    const existing = await Notifications.getPermissionsAsync();
    let granted = existing.granted;
    if (!granted && existing.canAskAgain) {
      granted = (await Notifications.requestPermissionsAsync()).granted;
    }
    if (!granted) return null;

    const id = projectId();
    if (!id) return null;

    const token = await Notifications.getExpoPushTokenAsync({ projectId: id });
    return looksLikePushToken(token.data) ? token.data : null;
  } catch {
    // A simulator has no APNs registration, and a development build without
    // the entitlement throws here. Neither is worth surfacing to the user.
    return null;
  }
}
