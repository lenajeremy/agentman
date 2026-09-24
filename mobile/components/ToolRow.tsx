import Feather from "@expo/vector-icons/Feather";
import { ComponentProps, useEffect, useMemo, useState } from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
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
import { parseDiff } from "../lib/diff";
import { Message } from "../lib/protocol";
import { font, Palette, radius, size, space } from "../lib/theme";
import { MotionPressable } from "./MotionPressable";
import { OutputViewer } from "./OutputViewer";

type FeatherName = ComponentProps<typeof Feather>["name"];

/** Output lines shown before the block asks to be opened fully. */
const OUTPUT_LINES = 12;
/** A command short enough to have fitted in the collapsed row already. */
const INLINE_COMMAND = 56;
/** A fixed-width column for the tile keeps every name on the same left edge. */
const TILE = 22;

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
 * The varying half of the row: which file, which pattern, which command.
 *
 * A path is shown from its tail. The answer to "what did it touch" is the
 * basename, the folder above it is the context, and the front of
 * /Users/mac/Desktop/… is the one part that is never what you were looking
 * for — yet it is exactly what a head-aligned truncation keeps.
 */
function isPath(summary: string): boolean {
  return /^[~.]?\/?[\w.@+-]+(\/[\w.@+-]+)+\/?$/.test(summary);
}

function Target({ summary }: { summary: string }) {
  const styles = useStyles(makeStyles);
  const first = summary.split("\n", 1)[0];

  if (isPath(first)) {
    const parts = first.replace(/\/$/, "").split("/");
    const base = parts.pop() ?? first;
    const parent = parts.length > 0 ? `${parts[parts.length - 1]}/` : "";
    return (
      <Text style={styles.target} numberOfLines={1}>
        <Text style={styles.targetDim}>{parent}</Text>
        {base}
      </Text>
    );
  }

  return (
    <Text style={styles.target} numberOfLines={1}>
      {first}
    </Text>
  );
}

/**
 * What the agent asked for, once the row is open.
 *
 * A rule and no fill, where output gets a fill and no rule: the two blocks are
 * the same monospace at the same size, so the ground has to carry the
 * difference between "this is what I ran" and "this is what came back".
 */
function CommandBlock({ text }: { text: string }) {
  const styles = useStyles(makeStyles);
  return (
    <View style={styles.command}>
      <Text style={styles.commandText} selectable>
        {text}
      </Text>
    </View>
  );
}

/**
 * What came back.
 *
 * Clamped to a dozen lines with the rest one tap away. Results are routinely a
 * hundred lines of grep hits, and a row that expands to fill four screens
 * costs more to scroll past than it ever gave back.
 */
function OutputBlock({
  name,
  command,
  text,
  failed,
}: {
  name: string;
  command?: string;
  text: string;
  failed: boolean;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [full, setFull] = useState(false);
  const [viewer, setViewer] = useState(false);

  const lines = useMemo(() => text.split("\n"), [text]);
  const hidden = full ? 0 : Math.max(0, lines.length - OUTPUT_LINES);
  const body = hidden > 0 ? lines.slice(0, OUTPUT_LINES).join("\n") : text;

  // A unified diff is the one output shape worth colouring: an agent's edit is
  // the change it made, and reading that as flat grey is reading it twice.
  const diff = useMemo(
    () => (body.includes("@@ -") ? parseDiff(body) : []),
    [body],
  );

  return (
    <View>
      {/* One Text per line, each wrapping within the row. Wrapping beats
          scrolling sideways here — a swipe to read the end of one line is a
          swipe the whole block has to make — but the lines stay separate
          elements with a hair of space between them, so a wrapped
          continuation still reads as part of the line above rather than as a
          new one. */}
      <View style={[styles.output, failed && styles.outputFailed]}>
        {diff.length > 0
          ? diff.map((row, i) => (
              <Text
                key={i}
                style={[
                  styles.outputText,
                  row.kind === "add" && styles.diffAdd,
                  row.kind === "remove" && styles.diffRemove,
                  row.kind === "hunk" && styles.diffHunk,
                ]}
                selectable
              >
                {row.kind === "add" ? "+" : row.kind === "remove" ? "−" : " "}
                {row.text}
              </Text>
            ))
          : body.split("\n").map((line, i) => (
              <Text
                key={i}
                style={[styles.outputText, failed && styles.outputTextFailed]}
                selectable
              >
                {line === "" ? " " : line}
              </Text>
            ))}
      </View>
      {/* Under the block rather than floating in its corner: a button pinned
          over the output covers the first line, which is the line most worth
          reading. Always offered, never conditional on the output being long:
          a control that comes and goes by a rule the reader cannot see is
          worse than one line of chrome, and roughly half of real tool calls
          land under any threshold worth setting. */}
      <View style={styles.footer}>
        {hidden > 0 ? (
          <Pressable
            onPress={() => setFull(true)}
            hitSlop={8}
            accessibilityRole="button"
            accessibilityLabel={`Show ${hidden} more lines inline`}
          >
            <Text style={styles.moreText}>
              {hidden === 1 ? "Show 1 more line" : `Show ${hidden} more lines`}
            </Text>
          </Pressable>
        ) : null}
        <Pressable
          onPress={() => setViewer(true)}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel={`Open the full ${name} output`}
        >
          <View style={styles.fullRow}>
            <Feather name="maximize-2" size={11} color={color.muted} />
            <Text style={styles.fullText}>Full screen</Text>
          </View>
        </Pressable>
      </View>

      <OutputViewer
        visible={viewer}
        onClose={() => setViewer(false)}
        name={name}
        command={command}
        output={text}
        failed={failed}
      />
    </View>
  );
}

/**
 * One tool call: what ran, and how it ended.
 *
 * The header is exactly one line, open or closed. It used to grow with the
 * command, which meant a `cd … && sed -n '…' | head -45` took four lines of
 * monospace before the output even started, and a feed of those is unreadable
 * — the header's job is to be scannable, and scanning wants a fixed height.
 * Opening the row reveals the body instead: the command in full, then the
 * output, each on its own ground.
 */
export function ToolRow({
  message,
  cwd = "",
}: {
  message: Message;
  cwd?: string;
}) {
  const [expanded, setExpanded] = useState(false);
  const styles = useStyles(makeStyles);
  const tool = message.tool;
  if (!tool) return null;

  const summary = tool.summary ?? "";
  const output = message.text ?? "";
  const failed = tool.status === "error";

  // The command only earns its own block when the header could not show it:
  // repeating a short path under itself is noise, and for a path the header
  // already shows the part worth reading.
  const showCommand =
    !isPath(summary) &&
    (summary.includes("\n") || summary.length > INLINE_COMMAND);
  const canOpen = Boolean(output) || showCommand;

  return (
    <View style={styles.row}>
      <MotionPressable
        onPress={() => {
          if (!canOpen) return;
          setExpanded((open) => !open);
        }}
        disabled={!canOpen}
        style={styles.header}
        pressedScale={0.995}
        accessibilityRole={canOpen ? "button" : "text"}
        accessibilityLabel={`${tool.name} ${summary}`}
        accessibilityState={{ expanded }}
      >
        <StatusTile status={tool.status} name={tool.name} />
        <Text style={[styles.name, failed && styles.nameFailed]}>
          {tool.name}
        </Text>
        {summary ? (
          <Target summary={summary} />
        ) : (
          <View style={styles.spacer} />
        )}
        {canOpen ? <Chevron expanded={expanded} /> : null}
      </MotionPressable>

      {expanded ? (
        <View style={styles.body}>
          {showCommand ? <CommandBlock text={summary} /> : null}
          {output ? (
            <OutputBlock
              name={tool.name}
              command={summary || undefined}
              text={output}
              failed={failed}
            />
          ) : null}
        </View>
      ) : null}
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    row: { marginHorizontal: -space.xs },
    header: {
      flexDirection: "row",
      alignItems: "center",
      gap: 10,
      minHeight: 30,
      borderRadius: radius.sm,
      paddingHorizontal: space.xs,
    },

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
    runningDot: {
      width: 6,
      height: 6,
      borderRadius: 3,
      backgroundColor: c.working,
    },
    arc: {
      width: 10,
      height: 10,
      borderRadius: 5,
      borderWidth: 1.5,
      borderColor: c.working,
      // One transparent edge turns a ring into an arc, so rotation is visible.
      borderTopColor: "transparent",
    },

    // The name is the repeated word down the feed and the target is the part
    // that differs, so the target gets the weight and the name steps back.
    // A minimum width lines the targets up into a second column, so the eye
    // runs down what differs instead of tracking a ragged left edge. Longer
    // names (WebFetch, TodoWrite) simply push past it rather than truncate.
    name: {
      minWidth: 46,
      fontFamily: font.mono,
      fontSize: size.label,
      color: c.muted,
    },
    nameFailed: { color: c.errorText },
    spacer: { flex: 1 },
    // flex lets a long target truncate instead of pushing the chevron off.
    target: {
      flex: 1,
      fontFamily: font.mono,
      fontSize: size.caption,
      color: c.textSecondary,
    },
    targetDim: { color: c.faint },
    chevron: { fontFamily: font.mono, fontSize: size.body, color: c.faint },

    body: {
      marginLeft: TILE + 10 + space.xs,
      marginTop: space.xs,
      gap: space.sm,
    },

    command: {
      borderLeftWidth: 2,
      borderLeftColor: c.workingSoft,
      paddingLeft: space.md,
      paddingVertical: space.xxs,
    },
    commandText: {
      fontFamily: font.mono,
      fontSize: 12,
      lineHeight: 18,
      color: c.textSecondary,
    },

    output: {
      backgroundColor: c.fill,
      borderRadius: radius.md,
      padding: space.md,
      // Just enough to separate one line from the wrapped remains of the
      // previous one without opening the block up into a list.
      gap: 3,
    },
    outputFailed: { backgroundColor: c.errorWash },
    outputText: {
      fontFamily: font.mono,
      fontSize: 12,
      lineHeight: 18,
      color: c.muted,
    },
    outputTextFailed: { color: c.errorText },

    diffAdd: { color: c.ok },
    diffRemove: { color: c.errorText },
    diffHunk: { color: c.faint },

    footer: {
      flexDirection: "row",
      gap: space.lg,
      alignItems: "center",
      paddingVertical: space.xs,
      paddingHorizontal: space.xxs,
    },
    moreText: {
      fontFamily: font.sansMedium,
      fontSize: size.label,
      color: c.working,
    },
    fullRow: { flexDirection: "row", alignItems: "center", gap: space.xs },
    fullText: {
      fontFamily: font.sansMedium,
      fontSize: size.label,
      color: c.muted,
    },
  });
