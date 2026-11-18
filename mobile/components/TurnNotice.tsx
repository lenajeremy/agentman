import Feather from "@expo/vector-icons/Feather";
import { StyleSheet, Text, View } from "react-native";

import { useStyles, useTheme } from "../lib/appearance";
import { font, Palette, radius, scaled, size, space } from "../lib/theme";

/**
 * How a turn ended when it didn't end with a reply: the plan's usage ran
 * out, or the agent's service refused it. Said plainly where the reply would
 * be, so a thread that stops there isn't taken for one still going.
 */
export function TurnNotice({ text, limit }: { text: string; limit: boolean }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const title = limit ? "Out of usage" : "The turn ended with an error";
  return (
    <View style={styles.card} accessible accessibilityRole="text" accessibilityLabel={`${title}. ${text}`}>
      <Feather name={limit ? "slash" : "alert-triangle"} size={14} color={color.error} style={styles.icon} />
      <View style={styles.words}>
        <Text style={styles.title}>{title}</Text>
        <Text style={styles.text} selectable>
          {text}
        </Text>
      </View>
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    card: {
      flexDirection: "row",
      alignItems: "flex-start",
      gap: space.sm,
      paddingHorizontal: space.md,
      paddingVertical: space.sm + 2,
      borderRadius: radius.md,
      borderWidth: StyleSheet.hairlineWidth * 2,
      borderColor: c.errorEdge,
      backgroundColor: c.errorWash,
    },
    icon: { marginTop: scaled(3) },
    words: { flex: 1, gap: 2 },
    title: { ...font.sansMedium, fontSize: size.caption, lineHeight: scaled(20), color: c.errorText },
    text: { ...font.sans, fontSize: size.caption, lineHeight: scaled(19), color: c.textSecondary },
  });
