import { StyleSheet, Text, View } from "react-native";

import { useStyles } from "../lib/appearance";
import { font, Palette, size, space } from "../lib/theme";
import { Loader } from "./Loader";

/**
 * Shown at the foot of the feed while the agent is mid-turn.
 *
 * Without it, a working agent and a finished one look identical: the last
 * message just sits there. That is the single most confusing moment in the
 * app, because "it is thinking" and "it is done and this is the answer" are
 * the two states the whole product exists to distinguish.
 *
 * The Mac's working ring, so an agent at work looks the same on both.
 */
export function Thinking({ label = "Working" }: { label?: string }) {
  const styles = useStyles(makeStyles);

  return (
    <View style={styles.row} accessibilityRole="progressbar" accessibilityLabel={label}>
      <Loader size={14} />
      <Text style={styles.label}>{label}</Text>
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    row: { flexDirection: "row", alignItems: "center", gap: space.sm },
    label: { ...font.sansMedium, fontSize: size.caption, color: c.muted },
  });
