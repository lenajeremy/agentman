import * as Haptics from "expo-haptics";
import { type ComponentRef, type ReactNode, type Ref, useEffect, useState } from "react";
import { ScrollView, StyleSheet, useWindowDimensions, View } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import Animated, {
  runOnJS,
  useAnimatedKeyboard,
  useAnimatedStyle,
  useSharedValue,
  withSpring,
  withTiming,
} from "react-native-reanimated";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useStyles } from "../lib/appearance";
import { type Palette, radius, space } from "../lib/theme";

/**
 * The sheet a question docks in, over the composer's place. Docked, it is as
 * tall as the question (up to a height that leaves the conversation readable
 * above it); pulled up by its grabber it covers the whole screen, header and
 * all, and pushed down it docks again. It follows the finger, and a flick
 * carries it the rest of the way, as iOS sheets do; a tap on the grabber does
 * the same.
 *
 * It is drawn over the screen rather than in the conversation's column, so
 * pulled up it can cover the header too. The column keeps a gap of the
 * docked height (onDockHeight) so the conversation ends above it.
 */
export function QuestionSheet({
  dockedHeight,
  bottomPadding,
  resetKey,
  scrollRef,
  onDockHeight,
  children,
}: {
  /** The tallest the sheet is when docked. */
  dockedHeight: number;
  bottomPadding: number;
  /** A new question docks again. */
  resetKey?: string;
  scrollRef?: Ref<ComponentRef<typeof ScrollView>>;
  /** How tall the docked sheet is, for the gap kept under the conversation. */
  onDockHeight?: (height: number) => void;
  children: ReactNode;
}) {
  const styles = useStyles(makeStyles);
  const insets = useSafeAreaInsets();
  const { height: viewport } = useWindowDimensions();
  const keyboard = useAnimatedKeyboard();
  const [expanded, setExpanded] = useState(false);
  const [contentHeight, setContentHeight] = useState<number | null>(null);

  // Docked, the sheet is as tall as the question, up to dockedHeight.
  const dock =
    contentHeight === null ? null : Math.min(contentHeight + GRABBER_AREA + bottomPadding, dockedHeight);
  const height = useSharedValue(0);
  const start = useSharedValue(0);
  const docked = useSharedValue(dockedHeight);
  const measured = useSharedValue(false);
  // Pulled up, it reaches the status bar.
  const full = useSharedValue(viewport - insets.top);

  useEffect(() => {
    if (dock !== null) onDockHeight?.(dock);
  }, [dock, onDockHeight]);

  useEffect(() => {
    full.value = viewport - insets.top;
  }, [viewport, insets.top, full]);

  useEffect(() => {
    if (dock === null) return;
    docked.value = dock;
    if (!measured.value) {
      measured.value = true;
      height.value = dock;
    } else if (!expanded) {
      // The question grew or shrank (a note opened, the next question came).
      height.value = withTiming(dock, { duration: 180 });
    }
  }, [dock, expanded, docked, measured, height]);

  useEffect(() => {
    setExpanded(false);
    height.value = withSpring(docked.value, SPRING);
  }, [resetKey, docked, height]);

  const settle = (open: boolean) => {
    setExpanded(open);
    Haptics.selectionAsync().catch(() => {});
  };

  const pan = Gesture.Pan()
    .onBegin(() => {
      start.value = height.value;
    })
    .onUpdate((event) => {
      const next = start.value - event.translationY;
      // Past either end it gives a little, then less, rather than stopping
      // dead: there is nothing more that way.
      if (next > full.value) {
        height.value = full.value + rubberband(next - full.value);
      } else if (next < docked.value) {
        height.value = docked.value - rubberband(docked.value - next);
      } else {
        height.value = next;
      }
    })
    .onEnd((event) => {
      // Where the flick would come to rest decides the side, not where the
      // finger let go (Apple's projection, scroll-deceleration rate).
      const projected = height.value - ((event.velocityY / 1000) * 0.998) / (1 - 0.998);
      const open = Math.abs(projected - full.value) < Math.abs(projected - docked.value);
      height.value = withSpring(open ? full.value : docked.value, { ...SPRING, velocity: -event.velocityY });
      runOnJS(settle)(open);
    });

  const tap = Gesture.Tap().onEnd(() => {
    const open = height.value < (docked.value + full.value) / 2;
    height.value = withSpring(open ? full.value : docked.value, SPRING);
    runOnJS(settle)(open);
  });

  const toggle = () => {
    const open = !expanded;
    height.value = withSpring(open ? full.value : docked.value, SPRING);
    settle(open);
  };

  const sheetStyle = useAnimatedStyle(() => {
    // It sits on the keyboard when one is up, and never reaches past the
    // status bar however far it is pulled.
    const room = full.value - keyboard.height.value;
    return measured.value
      ? { bottom: keyboard.height.value, height: Math.min(height.value, room + space.md) }
      : { bottom: keyboard.height.value };
  });

  return (
    <Animated.View
      style={[
        styles.sheet,
        { paddingBottom: bottomPadding },
        contentHeight === null ? { maxHeight: dockedHeight } : null,
        sheetStyle,
      ]}
    >
      <GestureDetector gesture={Gesture.Race(pan, tap)}>
        <View
          style={styles.grabberArea}
          accessible
          accessibilityRole="button"
          accessibilityLabel={expanded ? "Dock the question" : "Show the question full screen"}
          accessibilityState={{ expanded }}
          accessibilityActions={[{ name: "activate" }]}
          onAccessibilityAction={toggle}
        >
          <View style={styles.grabber} />
        </View>
      </GestureDetector>
      <ScrollView
        ref={scrollRef}
        style={styles.scroll}
        contentContainerStyle={styles.content}
        keyboardShouldPersistTaps="handled"
        nestedScrollEnabled
      >
        {/* Measured directly: the scroll view's own content-size event did
            not fire on the web build once the sheet floated. */}
        <View onLayout={(event) => setContentHeight(event.nativeEvent.layout.height + CONTENT_PADDING)}>
          {children}
        </View>
      </ScrollView>
    </Animated.View>
  );
}

/** The grabber's strip: a full-width target, tall enough for a thumb. */
const GRABBER_AREA = 28;

/** The content's own top and bottom padding (styles.content). */
const CONTENT_PADDING = space.xs * 2;

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
      position: "absolute",
      left: 0,
      right: 0,
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
      backgroundColor: c.fillStrong,
    },
    scroll: { flex: 1 },
    content: { paddingHorizontal: 20, paddingTop: space.xs, paddingBottom: space.xs },
  });
