import { View } from "react-native";

import { useBoardPrefs } from "../lib/board-prefs";
import type { Session } from "../lib/protocol";

/**
 * The small stroke at a row's left edge, in the colour the user picked to
 * tell agents apart: the same colour their cards on the Board have.
 */
export function AgentColorMark({ session }: { session: Pick<Session, "state" | "question" | "cwd"> }) {
  const { colorOf } = useBoardPrefs();
  const hue = colorOf(session);
  if (!hue) return null;
  return (
    <View
      accessibilityElementsHidden
      importantForAccessibility="no"
      pointerEvents="none"
      style={{ position: "absolute", left: 4, top: 16, bottom: 16, width: 3, borderRadius: 2, backgroundColor: hue }}
    />
  );
}
