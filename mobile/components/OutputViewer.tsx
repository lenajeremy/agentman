import Feather from "@expo/vector-icons/Feather";
import { useMemo, useState } from "react";
import { Modal, Pressable, ScrollView, Share, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { parseDiff } from "../lib/diff";
import { font, radius, size, space } from "../lib/theme";

/**
 * A terminal, not a themed surface.
 *
 * These colours are fixed rather than drawn from the palette because this is
 * the one screen that shows an agent's raw output verbatim, and every mental
 * model of that output — every screenshot of it, every place it was produced —
 * is light text on a dark ground. A light-mode rendering of `go test ./...` on
 * paper is technically consistent and looks nothing like the thing it is.
 */
const term = {
  ground: "#0B0C0E",
  raised: "#131519",
  rule: "#212429",
  text: "#D6D8DE",
  dim: "#787C86",
  prompt: "#3FBF87",
  add: "#2BC784",
  remove: "#FF8A8E",
  hunk: "#6E717A",
  error: "#FF8A8E",
};

interface Props {
  visible: boolean;
  onClose: () => void;
  name: string;
  command?: string;
  output: string;
  failed?: boolean;
}

/**
 * The whole of a tool's output, full screen.
 *
 * The row in the feed is a preview — twelve lines, enough to know whether this
 * is the call you were looking for. This is where you actually read it, so it
 * gets the screen, the terminal's own ground, and a wrap toggle: unwrapped is
 * right for the tabular output that dominates (grep hits, test results), and
 * wrapped is right for the one stack trace with a 300-character line.
 */
export function OutputViewer({ visible, onClose, name, command, output, failed }: Props) {
  const insets = useSafeAreaInsets();
  // Wrapped by default. Scrolling sideways to finish a line means scrolling
  // every other line with it, and on a phone that is a worse trade than a
  // continuation or two — but columnar output is still one tap away.
  const [wrap, setWrap] = useState(true);

  const lines = useMemo(() => output.split("\n"), [output]);
  const diff = useMemo(() => (output.includes("@@ -") ? parseDiff(output) : []), [output]);

  const rows =
    diff.length > 0
      ? diff.map((row, i) => ({
          key: i,
          text: `${row.kind === "add" ? "+" : row.kind === "remove" ? "−" : " "}${row.text}`,
          color:
            row.kind === "add"
              ? term.add
              : row.kind === "remove"
                ? term.remove
                : row.kind === "hunk"
                  ? term.hunk
                  : term.text,
        }))
      : lines.map((text, i) => ({
          key: i,
          text: text === "" ? " " : text,
          color: failed ? term.error : term.text,
        }));

  const body = (
    <View style={[styles.lines, wrap && styles.linesWrapped]}>
      {rows.map((row) => (
        <Text
          key={row.key}
          style={[styles.line, { color: row.color }]}
          numberOfLines={wrap ? undefined : 1}
          selectable
        >
          {row.text}
        </Text>
      ))}
    </View>
  );

  return (
    <Modal
      visible={visible}
      onRequestClose={onClose}
      animationType="slide"
      presentationStyle="fullScreen"
      statusBarTranslucent
    >
      <View style={[styles.screen, { paddingTop: insets.top }]}>
        <View style={styles.bar}>
          <Pressable onPress={onClose} hitSlop={12} style={styles.close} accessibilityRole="button"
            accessibilityLabel="Close output">
            <Feather name="x" size={18} color={term.text} />
          </Pressable>
          <View style={styles.title}>
            <Text style={styles.titleText} numberOfLines={1}>{name}</Text>
            <Text style={styles.titleMeta}>
              {lines.length === 1 ? "1 line" : `${lines.length} lines`}
            </Text>
          </View>
          {/* The share sheet rather than a clipboard call: the user picks the
              destination themselves, and a 300-line log is usually wanted
              somewhere other than this phone. */}
          <Pressable
            onPress={() => {
              void Share.share({
                message: command ? `$ ${command}\n\n${output}` : output,
              }).catch(() => {});
            }}
            hitSlop={12}
            style={styles.toggle}
            accessibilityRole="button"
            accessibilityLabel="Share this output"
          >
            <Feather name="share" size={14} color={term.dim} />
          </Pressable>
          <Pressable
            onPress={() => setWrap((on) => !on)}
            hitSlop={12}
            style={[styles.toggle, wrap && styles.toggleOn]}
            accessibilityRole="button"
            accessibilityLabel={wrap ? "Stop wrapping lines" : "Wrap lines"}
            accessibilityState={{ selected: wrap }}
          >
            <Feather name="corner-down-left" size={14} color={wrap ? term.ground : term.dim} />
          </Pressable>
        </View>

        {/* Wrapped and capped: this bar exists to say what produced the
            output, and a command long enough to need six lines is one the row
            in the feed already shows in full. */}
        {command ? (
          <View style={styles.command}>
            <Text style={styles.commandLine} numberOfLines={6} selectable>
              <Text style={styles.prompt}>$ </Text>
              {command}
            </Text>
          </View>
        ) : null}

        <ScrollView
          style={styles.scroll}
          contentContainerStyle={[styles.scrollInner, { paddingBottom: insets.bottom + space.xl }]}
        >
          {wrap ? (
            body
          ) : (
            <ScrollView horizontal showsHorizontalScrollIndicator={false}>
              {body}
            </ScrollView>
          )}
        </ScrollView>
      </View>
    </Modal>
  );
}

const styles = StyleSheet.create({
  screen: { flex: 1, backgroundColor: term.ground },

  bar: {
    flexDirection: "row",
    alignItems: "center",
    gap: space.sm,
    paddingHorizontal: space.lg,
    paddingVertical: space.md,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: term.rule,
  },
  close: { width: 28, height: 28, alignItems: "center", justifyContent: "center" },
  title: { flex: 1 },
  titleText: { fontFamily: font.monoMedium, fontSize: size.caption, color: term.text },
  titleMeta: { fontFamily: font.sans, fontSize: 11, color: term.dim, marginTop: 1 },
  toggle: {
    width: 30,
    height: 28,
    borderRadius: radius.sm,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: term.raised,
  },
  toggleOn: { backgroundColor: term.text },

  command: {
    paddingHorizontal: space.lg,
    paddingVertical: space.md,
    backgroundColor: term.raised,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: term.rule,
  },
  commandLine: { fontFamily: font.mono, fontSize: 12.5, lineHeight: 19, color: term.text },
  prompt: { color: term.prompt },

  scroll: { flex: 1 },
  scrollInner: { paddingTop: space.md },
  lines: { paddingHorizontal: space.lg },
  linesWrapped: { gap: 3 },
  line: { fontFamily: font.mono, fontSize: 12.5, lineHeight: 19 },
});
