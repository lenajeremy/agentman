import Feather from "@expo/vector-icons/Feather";
import { ComponentProps, useEffect } from "react";
import { StyleSheet, Text } from "react-native";
import Animated, { FadeInUp, FadeOut, useReducedMotion } from "react-native-reanimated";

import { useStyles, useTheme } from "../lib/appearance";
import { font, Palette, radius, size, space } from "../lib/theme";

/** How long a confirmation stays. Long enough to read one word, no longer. */
const SHOWN_MS = 1600;

/**
 * A short confirmation that something happened out of sight.
 *
 * A clipboard write leaves no trace on screen, so without this there is no
 * way to tell a copy worked short of pasting it somewhere. It is the same ink
 * pill as the Agents screen's undo bar, which is what the app already uses to
 * say "that happened", rather than a new kind of message to learn.
 *
 * Shows while `message` is set and calls onHidden when its time is up; set it
 * again to show it again.
 */
export function Toast({
  message,
  icon = "check",
  top,
  onHidden,
}: {
  message: string | null;
  icon?: ComponentProps<typeof Feather>["name"];
  /** Distance from the top of the screen. Placed under the header, where it
   *  never collides with the composer or the keyboard. */
  top: number;
  onHidden(): void;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const reduceMotion = useReducedMotion();

  useEffect(() => {
    if (!message) return;
    const timer = setTimeout(onHidden, SHOWN_MS);
    return () => clearTimeout(timer);
  }, [message, onHidden]);

  if (!message) return null;
  return (
    <Animated.View
      key={message}
      entering={reduceMotion ? undefined : FadeInUp.duration(180)}
      exiting={reduceMotion ? undefined : FadeOut.duration(160)}
      style={[styles.toast, { top }]}
      pointerEvents="none"
      accessibilityLiveRegion="polite"
      accessibilityRole="alert"
    >
      <Feather name={icon} size={15} color={color.onInverse} />
      <Text style={styles.text}>{message}</Text>
    </Animated.View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    toast: {
      position: "absolute",
      alignSelf: "center",
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      paddingVertical: space.sm + 2,
      paddingHorizontal: space.lg,
      borderRadius: radius.pill,
      backgroundColor: c.inverse,
      shadowColor: c.shadow,
      shadowOpacity: 0.2,
      shadowRadius: 20,
      shadowOffset: { width: 0, height: 10 },
      elevation: 10,
      zIndex: 10,
    },
    text: { fontFamily: font.sansMedium, fontSize: size.caption, color: c.onInverse },
  });
