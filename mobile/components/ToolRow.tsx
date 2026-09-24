import Feather from "@expo/vector-icons/Feather";
import { ComponentProps, useEffect, useState } from "react";
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

import { useStyles, useTheme } from "../lib/appearance";
import { Message } from "../lib/protocol";
import { font, Palette, radius, size, space } from "../lib/theme";
import { MotionPressable } from "./MotionPressable";

type FeatherName = ComponentProps<typeof Feather>["name"];

/**
 * A glyph for the kind of work a tool does. Names come straight from each
 * CLI (Claude's "Read", Codex's "shell", OpenCode's "webfetch"), so this
 * matches on meaning rather than exact spelling, and anything unknown gets a
 * neutral mark instead of a wrong one.
 */
export function toolIcon(name: string): FeatherName {
  const n = name.toLowerCase();
  if (/(bash|shell|exec|command|terminal)/.test(n)) return "terminal";
  if (/(edit|write|patch|notebook)/.test(n)) return "edit-3";
  if (/(read|view|cat|open)/.test(n)) return "file-text";
  if (/(grep|glob|search|find|list|ls)/.test(n)) return "search";
  if (/(web|fetch|http|url|browse)/.test(n)) return "globe";
  if (/(todo|plan)/.test(n)) return "check-square";
  if (/(task|agent)/.test(n)) return "git-branch";
  return "tool";
}

/**
 * What kind of tool ran and how it ended, in one fixed-size tile.
 *
 * Status used to be the characters ⏺ ◐ ✗. On the web those render as neat
 * little marks — but iOS gives U+23FA emoji presentation, so on a real phone it
 * became a heavy rounded square that sat off the text baseline. Drawing the
 * mark avoids font and platform variance, and the fixed tile keeps names and
 * commands lined up down the whole feed.
 */
function StatusTile({ status, name }: { status?: string; name: string }) {
  const spin = useSharedValue(0);
  const reduceMotion = useReducedMotion();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const running = status === "running";
  const failed = status === "error";

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

  // An open arc reads as motion the moment it turns; a filled dot would not.
  if (running) {
    return (
      <View style={[styles.tile, styles.tileRunning]}>
        {reduceMotion ? (
          <View style={styles.runningDot} />
        ) : (
          <Animated.View style={[styles.arc, spinStyle]} />
        )}
      </View>
    );
  }

  return (
    <View style={[styles.tile, failed && styles.tileFailed]}>
      <Feather
        name={failed ? "x" : toolIcon(name)}
        size={12}
        color={failed ? color.errorText : color.muted}
      />
    </View>
  );
}

function Chevron({ expanded }: { expanded: boolean }) {
  const styles = useStyles(makeStyles);
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
  const styles = useStyles(makeStyles);
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
        <StatusTile status={tool.status} name={tool.name} />
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

// A fixed-width column for the tile keeps every name on the same left edge.
const TILE = 22;

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    row: { borderRadius: radius.sm, marginHorizontal: -space.xs, paddingHorizontal: space.xs },
    line: { flexDirection: "row", alignItems: "center", gap: 10, minHeight: 28 },

    tile: {
      width: TILE,
      height: TILE,
      borderRadius: 7,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.fill,
    },
    tileRunning: { backgroundColor: c.workingWash },
    tileFailed: { backgroundColor: c.errorWash },
    runningDot: { width: 6, height: 6, borderRadius: 3, backgroundColor: c.working },
    arc: {
      width: 10,
      height: 10,
      borderRadius: 5,
      borderWidth: 1.5,
      borderColor: c.working,
      // One transparent edge turns a ring into an arc, so rotation is visible.
      borderTopColor: "transparent",
    },

    name: { fontFamily: font.monoMedium, fontSize: size.caption, color: c.text },
    nameFailed: { color: c.errorText },
    // flex lets a long command truncate instead of pushing the chevron off.
    summary: { flex: 1, fontFamily: font.mono, fontSize: size.caption, color: c.muted },
    chevron: {
      fontFamily: font.mono,
      fontSize: size.body,
      color: c.faint,
    },

    output: {
      fontFamily: font.mono,
      fontSize: 12,
      color: c.textSecondary,
      lineHeight: 18,
      marginTop: space.sm,
      marginLeft: TILE + 10,
      padding: space.md,
      backgroundColor: c.fill,
      borderRadius: radius.md,
      overflow: "hidden",
    },
  });
