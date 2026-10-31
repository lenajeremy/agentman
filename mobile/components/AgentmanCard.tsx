import Feather from "@expo/vector-icons/Feather";
import { useRouter } from "expo-router";
import { StyleSheet, Text, View } from "react-native";

import { useStyles, useTheme } from "../lib/appearance";
import { useManager } from "../lib/manager";
import { sessionNeedsAnswer } from "../lib/question-alerts";
import { useStore } from "../lib/store";
import { font, Palette, radius, size, space } from "../lib/theme";
import { BrandMark } from "./BrandMark";
import { MotionPressable } from "./MotionPressable";

/**
 * Agentman, pinned above the agents it manages: where the user spends most of
 * their time, so it stands apart from the list, as it does on the Mac.
 */
export function AgentmanCard() {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const manager = useManager();
  const store = useStore();
  const router = useRouter();
  if (!manager.supported) return null;

  const waiting = store.visibleSessions.filter(sessionNeedsAnswer).length;
  const working = store.visibleSessions.filter((session) => session.state === "busy").length;
  const cards = manager.openCards.length;
  const summary =
    cards > 0 ? (cards === 1 ? "1 card waiting for you" : `${cards} cards waiting for you`)
    : waiting > 0 ? (waiting === 1 ? "1 agent needs you" : `${waiting} agents need you`)
    : manager.thinking ? "Thinking…"
    : working > 0 ? (working === 1 ? "1 agent working" : `${working} agents working`)
    : "All quiet";
  const attention = cards > 0 || waiting > 0;

  return (
    <MotionPressable
      onPress={() => router.push("/agentman")}
      style={styles.card}
      pressedScale={0.985}
      accessibilityRole="button"
      accessibilityLabel={`Agentman. ${summary}${manager.unread > 0 ? `. ${manager.unread} new` : ""}`}
    >
      <BrandMark size={36} ring={color.surface} />
      <View style={styles.words}>
        <Text style={styles.name}>Agentman</Text>
        <Text style={[styles.summary, attention && styles.attention]} numberOfLines={1}>
          {summary}
        </Text>
      </View>
      {manager.unread > 0 ? (
        <View style={styles.badge}>
          <Text style={styles.badgeText}>{manager.unread > 99 ? "99+" : manager.unread}</Text>
        </View>
      ) : null}
      <Feather name="chevron-right" size={18} color={color.faint} />
    </MotionPressable>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    card: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.md,
      paddingVertical: space.md,
      paddingHorizontal: 14,
      marginTop: space.xs,
      marginBottom: space.lg,
      borderRadius: radius.xl,
      borderWidth: 1,
      borderColor: c.line,
      backgroundColor: c.surface,
    },
    words: { flex: 1, gap: 2 },
    name: { ...font.sansMedium, fontSize: size.body, color: c.text },
    summary: { ...font.sans, fontSize: size.caption, color: c.muted },
    attention: { color: c.needsYouText },
    badge: {
      minWidth: 22,
      height: 22,
      paddingHorizontal: 6,
      borderRadius: 11,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.working,
    },
    badgeText: { ...font.sansMedium, fontSize: size.label, color: c.onAccent },
  });
