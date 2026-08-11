import * as Haptics from "expo-haptics";
import { ReactNode, useCallback } from "react";
import { Platform, StyleSheet, Text, View } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import Animated, {
  runOnJS,
  useAnimatedStyle,
  useSharedValue,
  withTiming,
} from "react-native-reanimated";

import { color, font, radius, space } from "../lib/theme";

/** How far the row must travel before letting go dismisses it. */
const THRESHOLD = 96;
/** Past this, the row is gone regardless of distance — a fast flick is intent. */
const FLICK_VELOCITY = 800;
/** Wide enough that the row is off screen on any phone. */
const OFF_SCREEN = 1000;

interface Props {
  children: ReactNode;
  onDismiss(): void;
  /** When false the row still moves a little, but never dismisses. Used for
   *  busy and blocked agents, which must not be hideable. */
  enabled?: boolean;
  /** Announced to screen readers, which cannot swipe. */
  accessibilityLabel?: string;
}

/**
 * A row that can be swiped left to hide it.
 *
 * Left only, deliberately: a right swipe is the system back gesture on iOS, and
 * competing with it means the user's back gesture sometimes deletes a row.
 *
 * The row does not spring back on success. It stays off screen and collapses,
 * because the alternative — snapping back while the list re-renders without it
 * — reads as the row you swiped being replaced by a different one.
 */
export function SwipeToDismiss({
  children,
  onDismiss,
  enabled = true,
  accessibilityLabel,
}: Props) {
  const translateX = useSharedValue(0);
  const height = useSharedValue<number | undefined>(undefined);
  const measured = useSharedValue(0);

  const buzz = useCallback(() => {
    if (Platform.OS === "web") return;
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Medium).catch(() => {});
  }, []);

  const finish = useCallback(() => {
    onDismiss();
  }, [onDismiss]);

  const pan = Gesture.Pan()
    // Vertical scrolling through a list of agents is the common gesture and
    // must win; only a clearly horizontal drag takes over.
    .activeOffsetX([-16, 16])
    .failOffsetY([-12, 12])
    .onUpdate((event) => {
      if (event.translationX > 0) {
        // Rightward drag gets a fraction of the movement: enough to feel
        // attached to the finger, not enough to look like it will do anything.
        translateX.value = event.translationX * 0.2;
        return;
      }
      translateX.value = event.translationX;
    })
    .onEnd((event) => {
      const far = event.translationX < -THRESHOLD;
      const flicked = event.velocityX < -FLICK_VELOCITY && event.translationX < -24;

      if (enabled && (far || flicked)) {
        runOnJS(buzz)();
        translateX.value = withTiming(-OFF_SCREEN, { duration: 180 });
        // Collapsing the gap is what makes the list close up rather than
        // leaving a hole where the row used to be.
        height.value = withTiming(0, { duration: 180 }, (done) => {
          if (done) runOnJS(finish)();
        });
        return;
      }
      translateX.value = withTiming(0, { duration: 180 });
    });

  const rowStyle = useAnimatedStyle(() => ({
    transform: [{ translateX: translateX.value }],
  }));

  const containerStyle = useAnimatedStyle(() =>
    height.value === undefined ? {} : { height: height.value, overflow: "hidden" },
  );

  // The backdrop only fades in as the row travels, so a half-committed swipe
  // shows how close it is rather than flashing at the first pixel.
  const backdropStyle = useAnimatedStyle(() => {
    const progress = Math.min(1, Math.max(0, -translateX.value / THRESHOLD));
    return { opacity: enabled ? progress : 0 };
  });

  return (
    <Animated.View
      style={containerStyle}
      onLayout={(event) => {
        // Measured once. Re-measuring during the collapse would fight the
        // animation for control of the same value.
        if (measured.value === 0) {
          measured.value = 1;
          height.value = event.nativeEvent.layout.height;
        }
      }}
    >
      <Animated.View style={[styles.backdrop, backdropStyle]} pointerEvents="none">
        <Text style={styles.backdropText}>Hide</Text>
      </Animated.View>
      <GestureDetector gesture={pan}>
        <Animated.View
          style={rowStyle}
          accessibilityActions={enabled ? [{ name: "magicTap", label: "Hide" }] : []}
          onAccessibilityAction={(event) => {
            if (event.nativeEvent.actionName === "magicTap") finish();
          }}
          accessibilityLabel={accessibilityLabel}
        >
          {children}
        </Animated.View>
      </GestureDetector>
    </Animated.View>
  );
}

const styles = StyleSheet.create({
  backdrop: {
    ...StyleSheet.absoluteFillObject,
    marginHorizontal: space.lg,
    marginBottom: space.sm,
    borderRadius: radius.md,
    backgroundColor: color.error,
    alignItems: "flex-end",
    justifyContent: "center",
    paddingRight: space.lg,
  },
  backdropText: {
    color: "#fff",
    fontFamily: font.sansMedium,
    fontSize: 15,
  },
});
