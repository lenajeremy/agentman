import { useEffect } from "react";
import { StyleSheet, Text, View } from "react-native";
import Animated, {
  Easing,
  cancelAnimation,
  useAnimatedStyle,
  useReducedMotion,
  useSharedValue,
  withDelay,
  withRepeat,
  withSequence,
  withTiming,
} from "react-native-reanimated";

import { color, font, size, space } from "../lib/theme";

/**
 * Shown at the foot of the feed while the agent is mid-turn.
 *
 * Without it, a working agent and a finished one look identical: the last
 * message just sits there. That is the single most confusing moment in the
 * app, because "it is thinking" and "it is done and this is the answer" are
 * the two states the whole product exists to distinguish.
 *
 * Three dots rather than a spinner. A spinner says "the app is loading"; the
 * app is fine, and it is the agent that is busy — this reads as someone
 * composing a reply, which is what is actually happening.
 */
export function Thinking({ label = "Working" }: { label?: string }) {
  const reduceMotion = useReducedMotion();

  return (
    <View style={styles.row} accessibilityRole="progressbar" accessibilityLabel={label}>
      <View style={styles.dots}>
        {[0, 1, 2].map((index) => (
          <Dot key={index} index={index} still={reduceMotion} />
        ))}
      </View>
      <Text style={styles.label}>{label}</Text>
    </View>
  );
}

function Dot({ index, still }: { index: number; still: boolean }) {
  const opacity = useSharedValue(0.25);

  useEffect(() => {
    if (still) {
      opacity.value = 0.6;
      return;
    }
    // Staggered so the dots travel as a wave rather than blinking in unison,
    // which would read as an alert rather than activity.
    opacity.value = withDelay(
      index * 180,
      withRepeat(
        withSequence(
          withTiming(1, { duration: 360, easing: Easing.linear }),
          withTiming(0.25, { duration: 360, easing: Easing.linear }),
        ),
        -1,
        false,
      ),
    );
    return () => cancelAnimation(opacity);
  }, [index, still, opacity]);

  const style = useAnimatedStyle(() => ({ opacity: opacity.value }));

  return <Animated.View style={[styles.dot, style]} />;
}

const styles = StyleSheet.create({
  row: { flexDirection: "row", alignItems: "center", gap: space.sm },
  dots: { flexDirection: "row", gap: 5, alignItems: "center" },
  dot: {
    width: 6,
    height: 6,
    borderRadius: 3,
    // The same cyan the status dot uses for a working agent, so the two read
    // as one signal in different places.
    backgroundColor: color.working,
  },
  label: { fontFamily: font.sans, fontSize: size.caption, color: color.muted },
});
