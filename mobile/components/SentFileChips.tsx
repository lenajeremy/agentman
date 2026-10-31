import Feather from "@expo/vector-icons/Feather";
import { StyleSheet, Text, View } from "react-native";

import { useStyles, useTheme } from "../lib/appearance";
import { font, Palette, radius, scaled, space } from "../lib/theme";

/**
 * The files that went with a message, by name, over its bubble: the agent was
 * given each one's path on the Mac, which says nothing worth reading here.
 */
export function SentFileChips({ paths }: { paths: string[] }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <View style={styles.row}>
      {paths.map((path) => (
        <View key={path} style={styles.chip} accessibilityLabel={`Sent ${path.split("/").at(-1)}`}>
          <Feather name="file-text" size={14} color={color.muted} />
          <Text style={styles.name} numberOfLines={1} ellipsizeMode="middle">
            {path.split("/").at(-1)}
          </Text>
        </View>
      ))}
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    row: { flexDirection: "row", flexWrap: "wrap", justifyContent: "flex-end", gap: space.sm, marginBottom: space.xs },
    chip: {
      flexDirection: "row",
      alignItems: "center",
      gap: 6,
      maxWidth: 240,
      paddingHorizontal: space.md,
      paddingVertical: 8,
      borderRadius: radius.lg,
      backgroundColor: c.toolCard,
    },
    name: { flexShrink: 1, ...font.sansMedium, fontSize: scaled(13), color: c.text },
  });
