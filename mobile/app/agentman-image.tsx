import Feather from "@expo/vector-icons/Feather";
import { useLocalSearchParams, useRouter } from "expo-router";
import { StatusBar } from "expo-status-bar";
import { useEffect, useState } from "react";
import { ActivityIndicator, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { ImageViewer } from "../components/ImageViewer";
import { MotionPressable } from "../components/MotionPressable";
import { PhotoScrim } from "../components/PhotoScrim";
import { useManager } from "../lib/manager";
import { font, radius, size, space } from "../lib/theme";

/**
 * A picture the user sent Agentman, full screen on the photo viewer's own
 * black page: pinch to zoom, drag down to put it away, tap for the chrome.
 */
export default function AgentmanImage() {
  const { name } = useLocalSearchParams<{ name?: string }>();
  const manager = useManager();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const [uri, setUri] = useState<string | null>(null);
  const [missing, setMissing] = useState(false);
  const [chrome, setChrome] = useState(true);

  useEffect(() => {
    if (!name) {
      setMissing(true);
      return;
    }
    let current = true;
    void manager.keptImage(name).then((found) => {
      if (!current) return;
      if (found) setUri(found);
      else setMissing(true);
    });
    return () => {
      current = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name]);

  return (
    <View style={styles.page}>
      <StatusBar style="light" hidden={!chrome} animated />
      {uri ? (
        <ImageViewer uri={uri} onTap={() => setChrome((shown) => !shown)} onDismiss={() => router.back()} />
      ) : missing ? (
        <Text style={styles.missing}>This picture is no longer on your Mac.</Text>
      ) : (
        <ActivityIndicator style={styles.loading} color="#FFFFFF" />
      )}
      {chrome || !uri ? (
        <View style={[styles.bar, { paddingTop: insets.top + space.sm }]} pointerEvents="box-none">
          <PhotoScrim />
          <MotionPressable
            onPress={() => router.back()}
            style={styles.button}
            hitSlop={10}
            accessibilityRole="button"
            accessibilityLabel="Close image"
          >
            <Feather name="x" size={20} color="#FFFFFF" />
          </MotionPressable>
          <Text style={styles.title} numberOfLines={1}>
            Sent to Agentman
          </Text>
        </View>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  // Black regardless of theme: it is what a picture is shown on.
  page: { flex: 1, backgroundColor: "#000000" },
  loading: { flex: 1 },
  missing: {
    flex: 1,
    textAlign: "center",
    textAlignVertical: "center",
    paddingTop: "70%",
    paddingHorizontal: space.xl,
    fontFamily: font.sans,
    fontSize: size.body,
    color: "rgba(255, 255, 255, 0.7)",
  },
  bar: {
    position: "absolute",
    top: 0,
    left: 0,
    right: 0,
    flexDirection: "row",
    alignItems: "center",
    gap: space.md,
    paddingHorizontal: space.lg,
    paddingBottom: space.md,
  },
  button: {
    width: 34,
    height: 34,
    borderRadius: radius.pill,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: "rgba(255, 255, 255, 0.14)",
  },
  title: { flex: 1, fontFamily: font.sansMedium, fontSize: size.body, color: "#FFFFFF" },
});
