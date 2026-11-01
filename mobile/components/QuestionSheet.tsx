import * as Haptics from "expo-haptics";
import { type ComponentRef, type ReactNode, type Ref, useEffect, useState } from "react";
import { ScrollView, StyleSheet, View } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import Animated, {
  runOnJS,
  useAnimatedStyle,
  useSharedValue,
  withSpring,
} from "react-native-reanimated";

import { useStyles } from "../lib/appearance";
import { type Palette, radius, space } from "../lib/theme";

/**
 * The sheet a question docks in, over the composer's place. It starts at a
 * height that leaves the conversation readable above it; a question longer
 * than that can be pulled up to nearly the whole screen by its grabber, and
 * pushed back down. The sheet follows the finger, and a flick carries it the
 * rest of the way, as iOS sheets do. A tap on the grabber does the same.
 */
export function QuestionSheet({
  collapsedHeight,
  expandedHeight,
  bottomPadding,
  resetKey,
  scrollRef,
  children,
}: {
  /** The tallest the sheet is before it is pulled up. */
  collapsedHeight: number;
  /** The tallest it can be pulled to. */
  expandedHeight: number;
  bottomPadding: number;
  /** A new question starts collapsed again. */
  resetKey?: string;
  scrollRef?: Ref<ComponentRef<typeof ScrollView>>;
  children: ReactNode;
}) {
  const styles = useStyles(makeStyles);
  const [expanded, setExpanded] = useState(false);
  const [contentHeight, setContentHeight] = useState(0);
  const limit = useSharedValue(collapsedHeight);
  const start = useSharedValue(collapsedHeight);
  const collapsed = useSharedValue(collapsedHeight);
  const full = useSharedValue(Math.max(collapsedHeight, expandedHeight));

  // Only a question taller than the collapsed sheet has more to show.
  const chrome = GRABBER_AREA + bottomPadding;
  const canExpand = contentHeight + chrome > collapsedHeight + 1;

  useEffect(() => {
    collapsed.value = collapsedHeight;
    full.value = Math.max(collapsedHeight, expandedHeight);
    limit.value = expanded && canExpand ? full.value : collapsedHeight;
  }, [collapsedHeight, expandedHeight, expanded, canExpand, collapsed, full, limit]);

  useEffect(() => {
    setExpanded(false);
  }, [resetKey]);

  const settle = (open: boolean) => {
    setExpanded(open);
    Haptics.selectionAsync().catch(() => {});
  };

  const pan = Gesture.Pan()
    .enabled(canExpand)
    .onBegin(() => {
      start.value = limit.value;
    })
    .onUpdate((event) => {
      const next = start.value - event.translationY;
      // Past either end it gives a little, then less, rather than stopping
      // dead: there is nothing more that way.
      if (next > full.value) {
        limit.value = full.value + rubberband(next - full.value);
      } else if (next < collapsed.value) {
        limit.value = collapsed.value - rubberband(collapsed.value - next);
      } else {
        limit.value = next;
      }
    })
    .onEnd((event) => {
      // Where the flick would come to rest decides the side, not where the
      // finger let go (Apple's projection, scroll-deceleration rate).
      const projected = limit.value - ((event.velocityY / 1000) * 0.998) / (1 - 0.998);
      const open = Math.abs(projected - full.value) < Math.abs(projected - collapsed.value);
      limit.value = withSpring(open ? full.value : collapsed.value, {
        ...SPRING,
        velocity: -event.velocityY,
      });
      runOnJS(settle)(open);
    });

  const tap = Gesture.Tap()
    .enabled(canExpand)
    .onEnd(() => {
      const open = limit.value < (collapsed.value + full.value) / 2;
      limit.value = withSpring(open ? full.value : collapsed.value, SPRING);
      runOnJS(settle)(open);
    });

  const toggle = () => {
    const open = !expanded;
    limit.value = withSpring(open ? full.value : collapsed.value, SPRING);
    settle(open);
  };

  const sheetStyle = useAnimatedStyle(() => ({ maxHeight: limit.value }));

  return (
    <Animated.View style={[styles.sheet, { paddingBottom: bottomPadding }, sheetStyle]}>
      <GestureDetector gesture={Gesture.Race(pan, tap)}>
        <View
          style={styles.grabberArea}
          accessible={canExpand}
          accessibilityRole={canExpand ? "button" : undefined}
          accessibilityLabel={canExpand ? (expanded ? "Make the question smaller" : "Show the whole question") : undefined}
          accessibilityState={canExpand ? { expanded } : undefined}
          accessibilityActions={canExpand ? [{ name: "activate" }] : undefined}
          onAccessibilityAction={canExpand ? toggle : undefined}
        >
          <View style={[styles.grabber, canExpand && styles.grabberLive]} />
        </View>
      </GestureDetector>
      <ScrollView
        ref={scrollRef}
        style={styles.scroll}
        contentContainerStyle={styles.content}
        keyboardShouldPersistTaps="handled"
        nestedScrollEnabled
        onContentSizeChange={(_, height) => setContentHeight(height)}
      >
        {children}
      </ScrollView>
    </Animated.View>
  );
}

/** The grabber's strip: a full-width target, tall enough for a thumb. */
const GRABBER_AREA = 28;

/** Critically damped, quick: a sheet settling, not bouncing. */
const SPRING = { dampingRatio: 1, duration: 420 } as const;

function rubberband(overshoot: number): number {
  "worklet";
  const dimension = 300;
  const constant = 0.55;
  return (overshoot * dimension * constant) / (dimension + constant * Math.abs(overshoot));
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    sheet: {
      backgroundColor: c.surface,
      borderTopLeftRadius: radius.sheet,
      borderTopRightRadius: radius.sheet,
      shadowColor: c.shadow,
      shadowOpacity: 0.12,
      shadowRadius: 24,
      shadowOffset: { width: 0, height: -6 },
      elevation: 12,
    },
    grabberArea: {
      height: GRABBER_AREA,
      alignItems: "center",
      justifyContent: "center",
    },
    grabber: {
      width: 38,
      height: 5,
      borderRadius: 3,
      backgroundColor: c.fill,
    },
    // Darker when it does something.
    grabberLive: { backgroundColor: c.fillStrong },
    scroll: { flexShrink: 1 },
    content: { paddingHorizontal: 20, paddingTop: space.xs, paddingBottom: space.xs },
  });
