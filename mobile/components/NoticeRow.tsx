import Feather from "@expo/vector-icons/Feather";
import { StyleSheet, Text, View } from "react-native";

import { useStyles, useTheme } from "../lib/appearance";
import { noticeOf } from "../lib/notices";
import { font, Palette, size, space } from "../lib/theme";

/**
 * A system line in a thread, such as a background task ending, said the way
 * a step of the work is: in the same soft grey pill, rather than a centred
 * sentence breaking the column.
 */
export function NoticeRow({ text }: { text: string }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const notice = noticeOf(text);
  const icon = notice.tone === "failed" ? "x" : notice.tone === "done" ? "check" : "info";
  return (
    <View style={styles.row} accessible accessibilityRole="text"
      accessibilityLabel={notice.outcome ? `${notice.label}, ${notice.outcome}` : notice.label}>
      <View style={styles.tile}>
        <Feather name={icon} size={12} color={notice.tone === "failed" ? color.errorText : color.faint} />
      </View>
      {/* One line, as a pill must be: what ran gives way, how it ended
          stays. VoiceOver reads it all. */}
      <Text style={styles.text} numberOfLines={1}>
        {notice.label}
      </Text>
      {notice.outcome ? (
        <Text style={[styles.outcomeText, notice.tone === "failed" ? styles.failed : styles.outcome]} numberOfLines={1}>
          {`· ${notice.outcome}`}
        </Text>
      ) : null}
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    // The same pill and measure as a tool step, so the two read as one list.
    row: {
      alignSelf: "flex-start",
      maxWidth: "100%",
      flexDirection: "row",
      alignItems: "flex-start",
      gap: 8,
      marginVertical: -4,
      paddingLeft: 4 + space.xs,
      paddingRight: 14,
      paddingVertical: 6,
      borderRadius: 16,
      backgroundColor: c.toolCard,
    },
    tile: { width: 22, height: 20, alignItems: "center", justifyContent: "center" },
    text: { flexShrink: 1, fontFamily: font.sans, fontSize: size.caption, lineHeight: 20, color: c.textSecondary },
    outcomeText: { flexShrink: 0, marginLeft: -4, fontFamily: font.sans, fontSize: size.caption, lineHeight: 20 },
    outcome: { color: c.faint },
    failed: { color: c.errorText },
  });
