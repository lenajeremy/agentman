import { useEffect, useState } from "react";
import { StyleSheet, Text, View } from "react-native";
import Animated, {
  Easing,
  cancelAnimation,
  useAnimatedStyle,
  useReducedMotion,
  useSharedValue,
  withRepeat,
  withTiming,
} from "react-native-reanimated";

import { Message } from "../lib/protocol";
import { color, font, radius, size, space } from "../lib/theme";
import { MotionPressable } from "./MotionPressable";

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
  const reduceMotion = useReducedMotion();
  const running = status === "running";

  useEffect(() => {
    if (!running || reduceMotion) {
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
  }, [reduceMotion, running, spin]);

  const spinStyle = useAnimatedStyle(() => ({
    transform: [{ rotate: `${spin.value * 360}deg` }],
  }));

  if (running) {
    if (reduceMotion) {
      return (
        <View style={styles.mark}>
          <View style={[styles.dot, styles.dotRunning]} />
        </View>
      );
    }
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

function Chevron({ expanded }: { expanded: boolean }) {
  const rotation = useSharedValue(expanded ? 90 : 0);
  const reduceMotion = useReducedMotion();

  useEffect(() => {
    const next = expanded ? 90 : 0;
    rotation.value = reduceMotion
      ? next
      : withTiming(next, {
          duration: 150,
          easing: Easing.bezier(0.23, 1, 0.32, 1),
        });
  }, [expanded, reduceMotion, rotation]);

  const style = useAnimatedStyle(() => ({
    transform: [{ rotate: `${rotation.value}deg` }],
  }));

  return <Animated.Text style={[styles.chevron, style]}>›</Animated.Text>;
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
    <MotionPressable
      onPress={() => {
        if (!hasOutput) return;
        setExpanded((open) => !open);
      }}
      disabled={!hasOutput}
      style={styles.row}
      pressedScale={0.995}
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
        {hasOutput ? <Chevron expanded={expanded} /> : null}
      </View>

      {expanded && hasOutput ? (
        <Text style={styles.output} selectable>
          {message.text}
        </Text>
      ) : null}
    </MotionPressable>
  );
}

// A fixed-width column for the mark keeps every name on the same left edge.
const MARK_COLUMN = 16;

const styles = StyleSheet.create({
  row: { borderRadius: radius.sm, marginHorizontal: -space.xs, paddingHorizontal: space.xs },
  line: { flexDirection: "row", alignItems: "center", gap: space.sm },

  mark: {
    width: MARK_COLUMN,
    height: MARK_COLUMN,
    alignItems: "center",
    justifyContent: "center",
  },
  dot: { width: 6, height: 6, borderRadius: 3, backgroundColor: color.faint },
  dotRunning: { backgroundColor: color.working },
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
  },

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
