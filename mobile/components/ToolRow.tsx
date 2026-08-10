import { useEffect, useState } from "react";
import { LayoutAnimation, Platform, Pressable, StyleSheet, Text, View } from "react-native";
import Animated, {
  Easing,
  cancelAnimation,
  useAnimatedStyle,
  useSharedValue,
  withRepeat,
  withTiming,
} from "react-native-reanimated";

import { Message } from "../lib/protocol";
import { color, font, radius, size, space } from "../lib/theme";

/**
 * The status glyph, drawn rather than typed.
 *
 * It used to be the characters ⏺ ◐ ✗. On the web those render as neat little
 * marks, which is how they passed review — but iOS gives U+23FA emoji
 * presentation, so on a real phone it became a heavy rounded square that sat
 * off the text baseline and made every tool row look ragged. Drawing the shape
 * sidesteps font and platform variance entirely and lets it be centred in a
 * fixed-width column, so names and commands line up down the whole feed.
 */
function StatusMark({ status }: { status?: string }) {
  const spin = useSharedValue(0);
  const running = status === "running";

  useEffect(() => {
    if (!running) {
      cancelAnimation(spin);
      spin.value = 0;
      return;
    }
    spin.value = withRepeat(
      withTiming(1, { duration: 900, easing: Easing.linear }),
      -1,
      false,
    );
    return () => cancelAnimation(spin);
  }, [running, spin]);

  const spinStyle = useAnimatedStyle(() => ({
    transform: [{ rotate: `${spin.value * 360}deg` }],
  }));

  if (running) {
    // An open arc reads as motion the moment it turns; a filled dot would not.
    return (
      <View style={styles.mark}>
        <Animated.View style={[styles.arc, spinStyle]} />
      </View>
    );
  }

  if (status === "error") {
    return (
      <View style={styles.mark}>
        <View style={[styles.cross, styles.crossA]} />
        <View style={[styles.cross, styles.crossB]} />
      </View>
    );
  }

  return (
    <View style={styles.mark}>
      <View style={styles.dot} />
    </View>
  );
}

/**
 * One tool call: what ran, and how it ended.
 *
 * Collapsed to a single line by default. A feed is mostly tool calls, and the
 * useful question at a glance is "what did it run", not "what did it print" —
 * output is one tap away for when that changes.
 */
export function ToolRow({ message }: { message: Message }) {
  const [expanded, setExpanded] = useState(false);
  const tool = message.tool;
  if (!tool) return null;

  const hasOutput = Boolean(message.text);
  const failed = tool.status === "error";

  return (
    <Pressable
      onPress={() => {
        if (!hasOutput) return;
        // Animate the height change so output does not snap into place and
        // shove the rest of the feed.
        if (Platform.OS !== "web") {
          LayoutAnimation.configureNext(
            LayoutAnimation.create(180, LayoutAnimation.Types.easeInEaseOut, LayoutAnimation.Properties.opacity),
          );
        }
        setExpanded((open) => !open);
      }}
      style={({ pressed }) => [styles.row, pressed && hasOutput && styles.rowPressed]}
      accessibilityRole={hasOutput ? "button" : "text"}
      accessibilityLabel={`${tool.name} ${tool.summary ?? ""}`}
      accessibilityState={{ expanded }}
    >
      <View style={styles.line}>
        <StatusMark status={tool.status} />
        <Text style={[styles.name, failed && styles.nameFailed]}>{tool.name}</Text>
        {tool.summary ? (
          <Text style={styles.summary} numberOfLines={expanded ? undefined : 1}>
            {tool.summary}
          </Text>
        ) : null}
        {hasOutput ? (
          <Text style={[styles.chevron, expanded && styles.chevronOpen]}>›</Text>
        ) : null}
      </View>

      {expanded && hasOutput ? (
        <Text style={styles.output} selectable>
          {message.text}
        </Text>
      ) : null}
    </Pressable>
  );
}

// A fixed-width column for the mark keeps every name on the same left edge.
const MARK_COLUMN = 16;

const styles = StyleSheet.create({
  row: { borderRadius: radius.sm, marginHorizontal: -space.xs, paddingHorizontal: space.xs },
  rowPressed: { backgroundColor: color.sunken },
  line: { flexDirection: "row", alignItems: "center", gap: space.sm },

  mark: {
    width: MARK_COLUMN,
    height: MARK_COLUMN,
    alignItems: "center",
    justifyContent: "center",
  },
  dot: { width: 6, height: 6, borderRadius: 3, backgroundColor: color.faint },
  arc: {
    width: 10,
    height: 10,
    borderRadius: 5,
    borderWidth: 1.5,
    borderColor: color.working,
    // One transparent edge turns a ring into an arc, so rotation is visible.
    borderTopColor: "transparent",
  },
  cross: { position: "absolute", width: 10, height: 1.5, backgroundColor: color.error },
  crossA: { transform: [{ rotate: "45deg" }] },
  crossB: { transform: [{ rotate: "-45deg" }] },

  name: { fontFamily: font.monoMedium, fontSize: size.caption, color: color.text },
  nameFailed: { color: color.error },
  // flexShrink lets a long command truncate instead of pushing the chevron off.
  summary: { flex: 1, fontFamily: font.mono, fontSize: size.caption, color: color.muted },
  chevron: {
    fontFamily: font.mono,
    fontSize: size.caption,
    color: color.faint,
    transform: [{ rotate: "90deg" }],
  },
  chevronOpen: { transform: [{ rotate: "270deg" }] },

  output: {
    fontFamily: font.mono,
    fontSize: size.caption,
    color: color.muted,
    lineHeight: 17,
    marginTop: space.sm,
    marginLeft: MARK_COLUMN + space.sm,
    padding: space.sm,
    backgroundColor: color.sunken,
    borderRadius: radius.sm,
  },
});
