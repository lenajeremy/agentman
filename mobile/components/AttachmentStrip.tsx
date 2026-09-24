import Feather from "@expo/vector-icons/Feather";
import { Image, Pressable, ScrollView, StyleSheet, View } from "react-native";

import { useStyles, useTheme } from "../lib/appearance";
import type { Attachment } from "../lib/use-attachments";
import { Palette, radius, space } from "../lib/theme";

/**
 * The images riding with the next message.
 *
 * Thumbnails rather than filenames: a screenshot from the camera roll has no
 * name worth reading, and the only question before sending is whether it is
 * the right picture. They sit inside the composer's own border rather than
 * above it, so the composer stays one object instead of becoming two stacked
 * ones — which is what ChatGPT and Codex both do, and it is right.
 */
export function AttachmentStrip({
  attachments,
  onRemove,
}: {
  attachments: Attachment[];
  onRemove: (key: string) => void;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  if (attachments.length === 0) return null;

  return (
    <ScrollView
      horizontal
      showsHorizontalScrollIndicator={false}
      contentContainerStyle={styles.row}
      keyboardShouldPersistTaps="handled"
    >
      {attachments.map((item) => (
        <View key={item.key} style={styles.item}>
          <Image source={{ uri: item.uri }} style={styles.thumb} resizeMode="cover" />
          <Pressable
            onPress={() => onRemove(item.key)}
            style={styles.remove}
            hitSlop={10}
            accessibilityRole="button"
            accessibilityLabel="Remove this image"
          >
            <Feather name="x" size={11} color={color.onInverse} />
          </Pressable>
        </View>
      ))}
    </ScrollView>
  );
}

const THUMB = 64;

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    row: { gap: space.sm, paddingBottom: space.sm, paddingRight: space.sm },
    // Room at the top and right for the remove control to sit half off the
    // corner without covering the picture it belongs to.
    item: { paddingTop: 7, paddingRight: 7 },
    thumb: {
      width: THUMB,
      height: THUMB,
      borderRadius: radius.md,
      backgroundColor: c.fill,
    },
    remove: {
      position: "absolute",
      top: 0,
      right: 0,
      width: 21,
      height: 21,
      borderRadius: 11,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.inverse,
      borderWidth: 1.5,
      borderColor: c.surface,
    },
  });
