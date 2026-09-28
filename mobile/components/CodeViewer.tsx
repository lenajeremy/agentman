import Feather from "@expo/vector-icons/Feather";
import { useMemo, useState } from "react";
import { Modal, Pressable, ScrollView, Share, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useStyles, useTheme } from "../lib/appearance";
import { font, Palette, radius, size, space } from "../lib/theme";
import { SyntaxCode } from "./SyntaxCode";

/**
 * A code block from a reply, full screen.
 *
 * Deliberately not OutputViewer. That one is a terminal: fixed dark ground,
 * the `output-*` tokens, a diff's colours. This is source someone is reading,
 * which the system already treats as a different thing — it sits on the app's
 * own surface with the `syntax-*` tokens, and its lines are numbered, because
 * the reason to open a snippet full screen is usually to talk about a line of
 * it.
 */
export function CodeViewer({
  visible,
  onClose,
  source,
  language,
}: {
  visible: boolean;
  onClose(): void;
  source: string;
  /** The fence's language, when it named one. */
  language: string;
}) {
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  // Wrapped by default, as everywhere else on a phone: finishing a long line
  // sideways means dragging every other line with it.
  const [wrap, setWrap] = useState(true);
  const lines = useMemo(() => source.split("\n").length, [source]);

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
          <Pressable
            onPress={onClose}
            hitSlop={12}
            style={styles.close}
            accessibilityRole="button"
            accessibilityLabel="Close code"
          >
            <Feather name="x" size={18} color={color.text} />
          </Pressable>
          <View style={styles.title}>
            <Text style={styles.titleText} numberOfLines={1}>
              {language || "Code"}
            </Text>
            <Text style={styles.titleMeta}>
              {lines === 1 ? "1 line" : `${lines.toLocaleString()} lines`}
            </Text>
          </View>
          <Pressable
            onPress={() => {
              void Share.share({ message: source }).catch(() => {});
            }}
            hitSlop={12}
            style={styles.toggle}
            accessibilityRole="button"
            accessibilityLabel="Share this code"
          >
            <Feather name="share" size={14} color={color.muted} />
          </Pressable>
          <Pressable
            onPress={() => setWrap((on) => !on)}
            hitSlop={12}
            style={[styles.toggle, wrap && styles.toggleOn]}
            accessibilityRole="button"
            accessibilityLabel={wrap ? "Stop wrapping lines" : "Wrap lines"}
          >
            <Feather
              name="corner-down-left"
              size={14}
              color={wrap ? color.workingText : color.muted}
            />
          </Pressable>
        </View>
        <ScrollView
          contentContainerStyle={{ paddingBottom: insets.bottom + space.xxl }}
        >
          <SyntaxCode source={source} filename="" language={language} wrap={wrap} />
        </ScrollView>
      </View>
    </Modal>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    screen: { flex: 1, backgroundColor: c.paper },
    bar: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      paddingHorizontal: space.md,
      paddingVertical: space.sm,
      borderBottomWidth: StyleSheet.hairlineWidth,
      borderBottomColor: c.line,
    },
    close: {
      width: 36,
      height: 36,
      alignItems: "center",
      justifyContent: "center",
    },
    title: { flex: 1, minWidth: 0 },
    titleText: {
      fontFamily: font.monoMedium,
      fontSize: size.caption,
      color: c.text,
    },
    titleMeta: {
      marginTop: 1,
      fontFamily: font.mono,
      fontSize: size.label,
      color: c.faint,
    },
    toggle: {
      width: 34,
      height: 34,
      alignItems: "center",
      justifyContent: "center",
      borderRadius: radius.pill,
      backgroundColor: c.fill,
    },
    toggleOn: { backgroundColor: c.workingWash },
  });
