import Feather from "@expo/vector-icons/Feather";
import { useRouter } from "expo-router";
import { useEffect, useState } from "react";
import { Image, StyleSheet, Text, View } from "react-native";

import { useStyles, useTheme } from "../lib/appearance";
import { useStore } from "../lib/store";
import { font, Palette, radius, size, space } from "../lib/theme";
import { MotionPressable } from "./MotionPressable";

/**
 * The pictures sent with a message, where their paths would otherwise be.
 * Each is read back from the Mac, which keeps it; a tap opens it full
 * screen.
 */
export function SentImages({
  sessionId,
  paths,
}: {
  sessionId: string;
  paths: string[];
}) {
  const styles = useStyles(makeStyles);
  return (
    <View style={styles.row}>
      {paths.map((path) => (
        <SentImage key={path} sessionId={sessionId} path={path} />
      ))}
    </View>
  );
}

function SentImage({ sessionId, path }: { sessionId: string; path: string }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const store = useStore();
  const router = useRouter();
  const [uri, setUri] = useState("");
  const [gone, setGone] = useState(false);

  useEffect(() => {
    let current = true;
    void store
      .workspace(sessionId, "read_seen_file", path)
      .then((result) => {
        if (!current) return;
        if (result.image && result.mime)
          setUri(`data:${result.mime};base64,${result.image}`);
        else setGone(true);
      })
      .catch(() => {
        if (current) setGone(true);
      });
    return () => {
      current = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, path]);

  if (gone) {
    return (
      <View style={styles.gone}>
        <Feather name="image" size={13} color={color.muted} />
        <Text style={styles.goneText}>Sent image</Text>
      </View>
    );
  }
  return (
    <MotionPressable
      onPress={() =>
        router.push(
          `/file/${encodeURIComponent(sessionId)}?path=${encodeURIComponent(path)}&seen=1`,
        )
      }
      disabled={!uri}
      style={styles.tile}
      accessibilityRole="imagebutton"
      accessibilityLabel="Sent image. Open full screen"
    >
      {uri ? (
        <Image source={{ uri }} style={styles.image} resizeMode="cover" />
      ) : null}
    </MotionPressable>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    row: {
      alignSelf: "flex-end",
      flexDirection: "row",
      flexWrap: "wrap",
      justifyContent: "flex-end",
      gap: space.sm,
      marginBottom: space.sm,
      maxWidth: "84%",
    },
    tile: {
      width: 120,
      height: 120,
      borderRadius: radius.lg,
      overflow: "hidden",
      backgroundColor: c.fill,
      borderWidth: StyleSheet.hairlineWidth,
      borderColor: c.line,
    },
    image: { width: "100%", height: "100%" },
    gone: {
      flexDirection: "row",
      alignItems: "center",
      gap: 6,
      alignSelf: "flex-end",
    },
    goneText: { ...font.sans, fontSize: size.caption, color: c.muted },
  });
