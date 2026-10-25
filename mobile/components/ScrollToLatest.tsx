import Feather from "@expo/vector-icons/Feather";
import { StyleSheet, View } from "react-native";
import Animated, { FadeIn, FadeOut, useReducedMotion } from "react-native-reanimated";

import { useStyles, useTheme } from "../lib/appearance";
import { Palette } from "../lib/theme";
import { MotionPressable } from "./MotionPressable";

/** How far from the latest message, in points, before the way back shows:
 *  about when the latest has scrolled out of sight. */
export const SCROLL_AWAY = 250;

/**
 * For a conversation's inverted list: what the reader is looking at stays
 * put as messages arrive or a reply streams in, and the list only follows
 * the latest when they are within a line or two of it already.
 */
export const KEEP_READING = { minIndexForVisible: 0, autoscrollToTopThreshold: 80 };

/**
 * The way back to the latest message, for someone reading further up: a
 * small round button over the foot of the conversation, as chat apps have.
 */
export function ScrollToLatest({ visible, onPress }: { visible: boolean; onPress(): void }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const reduceMotion = useReducedMotion();
  if (!visible) return null;
  return (
    <View style={styles.anchor} pointerEvents="box-none">
      <Animated.View
        entering={reduceMotion ? undefined : FadeIn.duration(140)}
        exiting={reduceMotion ? undefined : FadeOut.duration(100)}
      >
        <MotionPressable
          onPress={onPress}
          style={styles.button}
          pressedScale={0.92}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel="Scroll to the latest message"
        >
          <Feather name="arrow-down" size={18} color={color.text} />
        </MotionPressable>
      </Animated.View>
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    anchor: { position: "absolute", left: 0, right: 0, bottom: 12, alignItems: "center" },
    button: {
      width: 38,
      height: 38,
      borderRadius: 19,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
      shadowColor: c.shadow,
      shadowOpacity: 0.12,
      shadowRadius: 10,
      shadowOffset: { width: 0, height: 4 },
      elevation: 3,
    },
  });
