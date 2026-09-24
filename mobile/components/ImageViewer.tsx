import { useState } from "react";
import { LayoutChangeEvent, StyleSheet, Text, View } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import Animated, {
  runOnJS,
  useAnimatedStyle,
  useSharedValue,
  withTiming,
} from "react-native-reanimated";

import { useTheme } from "../lib/appearance";
import { font, Palette } from "../lib/theme";

const MIN_SCALE = 1;
const MAX_SCALE = 8;
/** What a double tap jumps to, which is enough to read code in a screenshot. */
const DOUBLE_TAP_SCALE = 3;

/**
 * A pinch-and-pan image viewer.
 *
 * A screenshot is the common case here — an agent's UI capture, a diagram —
 * and a fixed-height, contained image is unreadable on a phone: the detail
 * that matters is a few pixels tall. Gestures run on the UI thread through
 * Reanimated so a pinch stays smooth while the transcript is still streaming.
 */
export function ImageViewer({ uri }: { uri: string }) {
  const { color } = useTheme();
  const styles = makeStyles(color);
  const [frame, setFrame] = useState({ width: 0, height: 0 });
  const [zoomed, setZoomed] = useState(false);

  const scale = useSharedValue(1);
  const savedScale = useSharedValue(1);
  const x = useSharedValue(0);
  const y = useSharedValue(0);
  const savedX = useSharedValue(0);
  const savedY = useSharedValue(0);

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
      runOnJS(setZoomed)(false);
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
    runOnJS(setZoomed)(savedScale.value > 1.01);
  };

  const pinch = Gesture.Pinch()
    .onUpdate((event) => {
      scale.value = savedScale.value * event.scale;
    })
    .onEnd(() => {
      savedScale.value = scale.value;
      settle();
    });

  // A single finger may only pan once there is something to pan to, so at rest
  // the gesture does not fight the page's own scrolling.
  const pan = Gesture.Pan()
    .averageTouches(true)
    .onUpdate((event) => {
      if (savedScale.value <= 1.01) return;
      x.value = savedX.value + event.translationX;
      y.value = savedY.value + event.translationY;
    })
    .onEnd(() => {
      if (savedScale.value <= 1.01) return;
      savedX.value = x.value;
      savedY.value = y.value;
      settle();
    });

  const doubleTap = Gesture.Tap()
    .numberOfTaps(2)
    .onEnd(() => {
      const target = savedScale.value > 1.01 ? MIN_SCALE : DOUBLE_TAP_SCALE;
      scale.value = withTiming(target);
      savedScale.value = target;
      x.value = withTiming(0);
      y.value = withTiming(0);
      savedX.value = 0;
      savedY.value = 0;
      runOnJS(setZoomed)(target > 1.01);
    });

  // Pinch and pan run together; the double tap only wins when neither began.
  const gesture = Gesture.Simultaneous(pinch, Gesture.Exclusive(doubleTap, pan));

  const animated = useAnimatedStyle(() => ({
    transform: [{ translateX: x.value }, { translateY: y.value }, { scale: scale.value }],
  }));

  const onLayout = (event: LayoutChangeEvent) => {
    const { width, height } = event.nativeEvent.layout;
    setFrame({ width, height });
  };

  return (
    <View style={styles.wrap}>
      <GestureDetector gesture={gesture}>
        <View style={styles.frame} onLayout={onLayout} collapsable={false}>
          <Animated.Image
            source={{ uri }}
            style={[styles.image, animated]}
            resizeMode="contain"
            accessibilityLabel="File preview. Pinch to zoom, drag to pan, double tap to fill."
          />
        </View>
      </GestureDetector>
      <Text style={styles.hint}>
        {zoomed ? "Drag to pan · double tap to fit" : "Pinch or double tap to zoom"}
      </Text>
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    wrap: { flex: 1 },
    // A dark ground rather than the page colour: it is the neutral every
    // screenshot sits on without tinting its edges, and it marks the image
    // area as a surface you can manipulate.
    frame: {
      flex: 1,
      minHeight: 420,
      backgroundColor: "#141518",
      overflow: "hidden",
      alignItems: "center",
      justifyContent: "center",
    },
    image: { width: "100%", height: "100%" },
    hint: {
      textAlign: "center",
      paddingVertical: 10,
      fontFamily: font.sans,
      fontSize: 11.5,
      color: c.faint,
    },
  });
