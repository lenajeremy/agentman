import Feather from "@expo/vector-icons/Feather";
import { ActivityIndicator, ScrollView, StyleSheet, Text, View } from "react-native";

import { useStyles, useTheme } from "../lib/appearance";
import { splitPath } from "../lib/mentions";
import { font, Palette, radius, size, space, scaled } from "../lib/theme";
import { MotionPressable } from "./MotionPressable";

/**
 * The session's files for an `@` being typed, over the message box: the name
 * of each, and the folder it is in. Picking one names it in the message.
 */
export function FileMenu({
  files,
  loading,
  typed,
  onPick,
}: {
  files: readonly string[];
  loading: boolean;
  /** Whether anything follows the `@` yet: with nothing, these are the
   *  files changed since the last commit. */
  typed: boolean;
  onPick: (path: string) => void;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  if (files.length === 0) {
    if (!loading && !typed) return null;
    return (
      <View style={styles.card}>
        {loading ? (
          <View style={styles.loading}>
            <ActivityIndicator size="small" color={color.faint} />
            <Text style={styles.note}>Looking for files…</Text>
          </View>
        ) : (
          <Text style={styles.note}>No file matches</Text>
        )}
      </View>
    );
  }
  return (
    <View style={styles.card}>
      <Text style={styles.label}>{typed ? "Files" : "Changed"}</Text>
      <ScrollView keyboardShouldPersistTaps="always" nestedScrollEnabled>
        {files.map((path) => {
          const { name, folder } = splitPath(path);
          return (
            <MotionPressable
              key={path}
              onPress={() => onPick(path)}
              style={styles.row}
              pressedScale={0.99}
              accessibilityRole="button"
              accessibilityLabel={`Mention ${path}`}
            >
              <Feather name="file-text" size={14} color={color.muted} />
              <Text style={styles.name} numberOfLines={1}>
                {name}
              </Text>
              {folder ? (
                <Text style={styles.folder} numberOfLines={1} ellipsizeMode="head">
                  {folder}
                </Text>
              ) : null}
            </MotionPressable>
          );
        })}
      </ScrollView>
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    card: {
      maxHeight: 300,
      marginBottom: space.sm,
      paddingVertical: space.xs,
      borderRadius: radius.lg,
      backgroundColor: c.surface,
      borderWidth: StyleSheet.hairlineWidth,
      borderColor: c.line,
      shadowColor: "#000000",
      shadowOpacity: 0.08,
      shadowRadius: 16,
      shadowOffset: { width: 0, height: 4 },
      elevation: 3,
    },
    label: {
      ...font.sansMedium,
      fontSize: size.label,
      color: c.faint,
      paddingHorizontal: space.md,
      paddingTop: space.xs,
      paddingBottom: 2,
    },
    row: { flexDirection: "row", alignItems: "center", gap: space.sm, paddingHorizontal: space.md, paddingVertical: 9 },
    name: { ...font.sansMedium, fontSize: size.caption, color: c.text, flexShrink: 0, maxWidth: "60%" },
    folder: { flex: 1, ...font.sans, fontSize: scaled(12), color: c.faint },
    loading: { flexDirection: "row", alignItems: "center", gap: space.sm, padding: space.md },
    note: { ...font.sans, fontSize: size.caption, color: c.muted, padding: space.md },
  });
