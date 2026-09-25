import { useState } from "react";
import { LayoutChangeEvent, StyleSheet, View } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import Animated, {
  runOnJS,
  useAnimatedStyle,
  useSharedValue,
  withTiming,
} from "react-native-reanimated";

const MIN_SCALE = 1;
const MAX_SCALE = 8;
/** What a double tap jumps to, which is enough to read code in a screenshot. */
const DOUBLE_TAP_SCALE = 3;
/** How far an unzoomed downward drag goes before it counts as "put this away". */
const DISMISS_DISTANCE = 120;

/**
 * A photo viewer, in the sense Apple's Photos means it.
 *
 * The image owns the whole screen on black, and everything else is either
 * overlaid on it or gone. What was here before was an image box inside a
 * scrolling page: a fixed 420pt frame that left a screenshot letterboxed in
 * the middle with a third of the display empty beneath it, which is the
 * opposite of what you open a picture for.
 *
 * Black rather than a themed background because that is what a photo is
 * shown on — it tints nothing at the edges and it is the only ground that
 * disappears.
 */
export function ImageViewer({
  uri,
  onTap,
  onDismiss,
}: {
  uri: string;
  /** Called on a single tap, for hiding and showing the chrome over the image. */
  onTap?: () => void;
  /** Called when the image is flung downward while unzoomed. */
  onDismiss?: () => void;
}) {
  const [frame, setFrame] = useState({ width: 0, height: 0 });

  const scale = useSharedValue(1);
  const savedScale = useSharedValue(1);
  const x = useSharedValue(0);
  const y = useSharedValue(0);
  const savedX = useSharedValue(0);
  const savedY = useSharedValue(0);
  /** Fades the ground as the image is dragged away, the way Photos does. */
  const dismissProgress = useSharedValue(0);

  /**
   * How far the image may travel at a given scale: half the overflow in each
   * direction, so an edge can always be brought back to the frame edge and
   * never flung past it.
   */
  const bound = (atScale: number, size: number) => {
    "worklet";
    return Math.max(0, (size * atScale - size) / 2);
  };

  const settle = () => {
    "worklet";
    if (scale.value < MIN_SCALE) {
      scale.value = withTiming(MIN_SCALE);
      savedScale.value = MIN_SCALE;
      x.value = withTiming(0);
      y.value = withTiming(0);
      savedX.value = 0;
      savedY.value = 0;
      return;
    }
    if (scale.value > MAX_SCALE) {
      scale.value = withTiming(MAX_SCALE);
      savedScale.value = MAX_SCALE;
    }
    const limitX = bound(savedScale.value, frame.width);
    const limitY = bound(savedScale.value, frame.height);
    const nextX = Math.min(Math.max(x.value, -limitX), limitX);
    const nextY = Math.min(Math.max(y.value, -limitY), limitY);
    if (nextX !== x.value) x.value = withTiming(nextX);
    if (nextY !== y.value) y.value = withTiming(nextY);
    savedX.value = nextX;
    savedY.value = nextY;
  };

  const pinch = Gesture.Pinch()
    .onUpdate((event) => {
      scale.value = savedScale.value * event.scale;
    })
    .onEnd(() => {
      savedScale.value = scale.value;
      settle();
    });

  const zoomed = () => {
    "worklet";
    return savedScale.value > 1.01;
  };

  // One gesture, two jobs, decided by whether the image is zoomed: panning the
  // picture when there is more of it than fits, and putting it away when there
  // is not. That is the rule Photos uses and the one a thumb already expects.
  const pan = Gesture.Pan()
    .averageTouches(true)
    .onUpdate((event) => {
      if (zoomed()) {
        x.value = savedX.value + event.translationX;
        y.value = savedY.value + event.translationY;
        return;
      }
      if (!onDismiss || event.translationY <= 0) return;
      y.value = event.translationY;
      x.value = event.translationX;
      dismissProgress.value = Math.min(1, event.translationY / (DISMISS_DISTANCE * 2));
    })
    .onEnd((event) => {
      if (zoomed()) {
        savedX.value = x.value;
        savedY.value = y.value;
        settle();
        return;
      }
      if (onDismiss && event.translationY > DISMISS_DISTANCE) {
        runOnJS(onDismiss)();
        return;
      }
      x.value = withTiming(0);
      y.value = withTiming(0);
      dismissProgress.value = withTiming(0);
    });

  const doubleTap = Gesture.Tap()
    .numberOfTaps(2)
    .onEnd(() => {
      const target = zoomed() ? MIN_SCALE : DOUBLE_TAP_SCALE;
      scale.value = withTiming(target);
      savedScale.value = target;
      x.value = withTiming(0);
      y.value = withTiming(0);
      savedX.value = 0;
      savedY.value = 0;
    });

  const singleTap = Gesture.Tap()
    .numberOfTaps(1)
    .onEnd(() => {
      if (onTap) runOnJS(onTap)();
    });

  // The single tap must wait to be sure it is not the first half of a double
  // one, or every zoom would flash the chrome on its way past.
  const taps = Gesture.Exclusive(doubleTap, singleTap);
  const gesture = Gesture.Simultaneous(pinch, Gesture.Race(pan, taps));

  const animated = useAnimatedStyle(() => ({
    transform: [{ translateX: x.value }, { translateY: y.value }, { scale: scale.value }],
  }));
  const ground = useAnimatedStyle(() => ({ opacity: 1 - dismissProgress.value * 0.6 }));

  const onLayout = (event: LayoutChangeEvent) => {
    const { width, height } = event.nativeEvent.layout;
    setFrame({ width, height });
  };

  return (
    <GestureDetector gesture={gesture}>
      <View style={styles.frame} onLayout={onLayout} collapsable={false}>
        <Animated.View style={[StyleSheet.absoluteFill, styles.ground, ground]} />
        <Animated.Image
          source={{ uri }}
          style={[styles.image, animated]}
          resizeMode="contain"
          accessibilityLabel="Image. Pinch to zoom, drag to pan, double tap to fill, drag down to close."
        />
      </View>
    </GestureDetector>
  );
}

const styles = StyleSheet.create({
  frame: { flex: 1, overflow: "hidden", alignItems: "center", justifyContent: "center" },
  ground: { backgroundColor: "#000000" },
  image: { width: "100%", height: "100%" },
});
