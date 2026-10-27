import { ActivityIndicator, ScrollView, StyleSheet, Text, View } from "react-native";

import { useStyles, useTheme } from "../lib/appearance";
import { commandLabel } from "../lib/commands";
import type { SlashCommand } from "../lib/protocol";
import { font, Palette, radius, size, space, scaled } from "../lib/theme";
import { MotionPressable } from "./MotionPressable";

/**
 * The "/" menu, over the message box while a command's name is being typed:
 * what the agent's own terminal would offer, best match first. Picking one
 * puts it in the box, ready for its argument or to send.
 */
export function SlashMenu({
  commands,
  loading,
  error,
  onPick,
}: {
  commands: readonly SlashCommand[];
  loading: boolean;
  error?: string;
  onPick: (command: SlashCommand) => void;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  if (!loading && !error && commands.length === 0) {
    return (
      <View style={styles.card}>
        <Text style={styles.note}>No command matches</Text>
      </View>
    );
  }
  return (
    <View style={styles.card}>
      {loading && commands.length === 0 ? (
        <View style={styles.loading}>
          <ActivityIndicator size="small" color={color.faint} />
          <Text style={styles.note}>Asking your Mac for commands…</Text>
        </View>
      ) : error && commands.length === 0 ? (
        <Text style={styles.note}>{error}</Text>
      ) : (
        <ScrollView keyboardShouldPersistTaps="always" nestedScrollEnabled>
          {commands.map((command) => (
            <MotionPressable
              key={commandLabel(command)}
              onPress={() => onPick(command)}
              style={styles.row}
              pressedScale={0.99}
              accessibilityRole="button"
              accessibilityLabel={`${commandLabel(command)}${command.description ? `. ${command.description}` : ""}`}
            >
              <Text style={styles.name} numberOfLines={1}>
                {commandLabel(command)}
                {command.argumentHint ? <Text style={styles.hint}>{`  ${command.argumentHint}`}</Text> : null}
              </Text>
              {command.description ? (
                <Text style={styles.description} numberOfLines={1}>
                  {command.description}
                </Text>
              ) : null}
            </MotionPressable>
          ))}
        </ScrollView>
      )}
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
    row: { paddingHorizontal: space.md, paddingVertical: 7, gap: 1 },
    name: { fontFamily: font.monoMedium, fontSize: size.caption, color: c.text },
    hint: { fontFamily: font.mono, color: c.faint },
    description: { fontFamily: font.sans, fontSize: scaled(12.5), color: c.muted, lineHeight: scaled(17) },
    loading: { flexDirection: "row", alignItems: "center", gap: space.sm, padding: space.md },
    note: { fontFamily: font.sans, fontSize: size.caption, color: c.muted, padding: space.md },
  });
