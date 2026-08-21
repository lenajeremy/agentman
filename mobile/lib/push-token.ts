/**
 * Shape check for an Expo push token.
 *
 * Kept in its own module with no React Native imports so it can be tested on
 * Node directly, the way the other pure rules in this directory are.
 *
 * This is a courtesy check, not a security boundary. The daemon runs the same
 * rules in internal/push and is the side that matters: it is what hands the
 * token to a third-party API, so it rejects a malformed one regardless of what
 * the app believes. Checking here only avoids a pointless round trip.
 */

/** Mirrors ValidToken in internal/push/push.go. */
export function looksLikePushToken(value: string): boolean {
  return (
    value.length >= 20 &&
    value.length <= 256 &&
    (value.startsWith("ExponentPushToken[") || value.startsWith("ExpoPushToken[")) &&
    value.endsWith("]")
  );
}
