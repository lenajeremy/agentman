import { createContext, ReactNode, useContext, useEffect, useMemo, useState } from "react";
import { BackHandler, Pressable, StyleSheet, useWindowDimensions, View } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import Animated, {
  Easing,
  runOnJS,
  useAnimatedStyle,
  useReducedMotion,
  useSharedValue,
  withTiming,
} from "react-native-reanimated";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useStyles } from "../lib/appearance";
import { Palette, radius } from "../lib/theme";

/** How far down the sheet starts, under the status bar: the Board shows
 *  above it. */
export const boardSheetTop = (insetTop: number) => insetTop + 14;

/** Dragged this far, or flicked this fast, it closes. */
const DRAG_TO_CLOSE = 120;
const FLICK_TO_CLOSE = 800;

const DragContext = createContext<ReturnType<typeof Gesture.Pan> | null>(null);

/**
 * A sheet over the Board, dragged down by its top bar to put away. Drawn in
 * the page rather than as a native sheet, so its keyboard handling and the
 * inverted conversation inside behave the same on every phone. Its content
 * stays mounted once opened, so a half-written message survives putting it
 * away.
 */
export function BoardSheet({
  open,
  onClose,
  children,
}: {
  open: boolean;
  onClose(): void;
  children: ReactNode;
}) {
  const styles = useStyles(makeStyles);
  const { height } = useWindowDimensions();
  const insets = useSafeAreaInsets();
  const reduceMotion = useReducedMotion();
  const offset = useSharedValue(open ? 0 : height);
  const [mounted, setMounted] = useState(open);
  const [shown, setShown] = useState(open);

  useEffect(() => {
    if (open) {
      setMounted(true);
      setShown(true);
    }
    const target = open ? 0 : height;
    if (reduceMotion) {
      offset.value = target;
      if (!open) setShown(false);
      return;
    }
    offset.value = withTiming(
      target,
      { duration: open ? 300 : 240, easing: Easing.out(Easing.cubic) },
      (finished) => {
        if (finished && !open) runOnJS(setShown)(false);
      },
    );
  }, [open, height, reduceMotion, offset]);

  // Android's back button puts the sheet away before it leaves the Board.
  useEffect(() => {
    if (!open) return;
    const subscription = BackHandler.addEventListener("hardwareBackPress", () => {
      onClose();
      return true;
    });
    return () => subscription.remove();
  }, [open, onClose]);

  const pan = useMemo(
    () =>
      Gesture.Pan()
        .activeOffsetY(6)
        .failOffsetX([-24, 24])
        .onUpdate((event) => {
          offset.value = Math.max(0, event.translationY);
        })
        .onEnd((event) => {
          if (event.translationY > DRAG_TO_CLOSE || event.velocityY > FLICK_TO_CLOSE) runOnJS(onClose)();
          else offset.value = withTiming(0, { duration: 200, easing: Easing.out(Easing.cubic) });
        }),
    [offset, onClose],
  );

  const sheetStyle = useAnimatedStyle(() => ({ transform: [{ translateY: offset.value }] }));
  const scrimStyle = useAnimatedStyle(() => ({ opacity: Math.max(0, 1 - offset.value / Math.max(1, height)) }));

  return (
    <View
      style={[StyleSheet.absoluteFill, !shown && styles.hidden]}
      pointerEvents={open ? "box-none" : "none"}
    >
      <Animated.View style={[StyleSheet.absoluteFill, styles.scrim, scrimStyle]}>
        <Pressable style={StyleSheet.absoluteFill} onPress={onClose} accessibilityLabel="Close Agentman" />
      </Animated.View>
      <Animated.View
        style={[styles.sheet, { top: boardSheetTop(insets.top) }, sheetStyle]}
        accessibilityViewIsModal={open}
      >
        {mounted ? <DragContext.Provider value={pan}>{children}</DragContext.Provider> : null}
      </Animated.View>
    </View>
  );
}

/** The sheet's drag handle and whatever sits beside it, which drag it. */
export function BoardSheetHeader({ children }: { children: ReactNode }) {
  const styles = useStyles(makeStyles);
  const pan = useContext(DragContext);
  const content = (
    <View>
      <View style={styles.handleRow}><View style={styles.handle} /></View>
      {children}
    </View>
  );
  return pan ? <GestureDetector gesture={pan}>{content}</GestureDetector> : content;
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    hidden: { display: "none" },
    scrim: { backgroundColor: c.scrim },
    sheet: {
      position: "absolute",
      left: 0,
      right: 0,
      bottom: 0,
      borderTopLeftRadius: radius.sheet,
      borderTopRightRadius: radius.sheet,
      backgroundColor: c.surface,
      overflow: "hidden",
    },
    handleRow: { alignItems: "center", paddingTop: 8 },
    handle: { width: 36, height: 5, borderRadius: 3, backgroundColor: c.fillStrong },
  });
