import { useRouter } from "expo-router";
import { useEffect, useState } from "react";
import { ActivityIndicator, Image, StyleSheet } from "react-native";

import { useStyles, useTheme } from "../lib/appearance";
import type { ImageTarget } from "../lib/imagepath";
import { useStore } from "../lib/store";
import { Palette, radius } from "../lib/theme";
import { MotionPressable } from "./MotionPressable";

/** The thumbnail's height; its width follows the picture's shape. */
const HEIGHT = 140;

/** Pictures already read, so scrolling back past one does not fetch it again. */
const seen = new Map<string, { uri: string; aspect: number }>();

/**
 * The picture a tool call read, under the call's card, as on the Mac. It is
 * read from the Mac when its row first appears; a tap opens it full screen.
 */
export function SeenImage({ sessionId, target }: { sessionId: string; target: ImageTarget }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const store = useStore();
  const router = useRouter();
  const key = `${sessionId}|${target.request}|${target.path}`;
  const [picture, setPicture] = useState(seen.get(key));
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    if (seen.has(key)) return;
    let current = true;
    let retry: ReturnType<typeof setTimeout> | undefined;
    // A daemon that has just started may not know the agent's files yet, so
    // it is asked a few times before the thumbnail gives up.
    const read = (attempt: number) => {
      void store
        .workspace(sessionId, target.request, target.path)
        .then((result) => {
          if (!current) return;
          if (!result.image || !result.mime) throw new Error("not an image");
          const uri = `data:${result.mime};base64,${result.image}`;
          Image.getSize(
            uri,
            (width, height) => {
              const next = { uri, aspect: height > 0 ? width / height : 1 };
              seen.set(key, next);
              if (current) setPicture(next);
            },
            () => {
              if (current) setFailed(true);
            },
          );
        })
        .catch(() => {
          if (!current) return;
          if (attempt < 3) retry = setTimeout(() => read(attempt + 1), 1500);
          else setFailed(true);
        });
    };
    read(0);
    return () => {
      current = false;
      if (retry) clearTimeout(retry);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  if (failed) return null;
  const width = picture ? Math.min(260, Math.max(96, HEIGHT * picture.aspect)) : 170;
  return (
    <MotionPressable
      onPress={() =>
        router.push(
          target.request === "read_seen_file"
            ? `/file/${encodeURIComponent(sessionId)}?path=${encodeURIComponent(target.path)}&seen=1`
            : `/file/${encodeURIComponent(sessionId)}?path=${encodeURIComponent(target.path)}`,
        )
      }
      disabled={!picture}
      style={[styles.tile, { width }]}
      accessibilityRole="imagebutton"
      accessibilityLabel={`${target.path.split("/").at(-1) ?? "Image"}. Open full screen`}
    >
      {picture ? (
        <Image source={{ uri: picture.uri }} style={styles.image} resizeMode="cover" />
      ) : (
        <ActivityIndicator size="small" color={color.faint} />
      )}
    </MotionPressable>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    tile: {
      height: HEIGHT,
      marginLeft: 12,
      borderRadius: radius.md,
      overflow: "hidden",
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.fill,
      borderWidth: StyleSheet.hairlineWidth,
      borderColor: c.line,
    },
    image: { width: "100%", height: "100%" },
  });
