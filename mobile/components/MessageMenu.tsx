import Feather from "@expo/vector-icons/Feather";
import { BlurView } from "expo-blur";
import { ComponentProps, ReactNode, useCallback, useEffect, useRef, useState } from "react";
import {
  Modal,
  Platform,
  Pressable,
  StyleProp,
  StyleSheet,
  Text,
  useWindowDimensions,
  View,
  ViewStyle,
} from "react-native";
import Animated, {
  Easing,
  interpolate,
  runOnJS,
  useAnimatedStyle,
  useReducedMotion,
  useSharedValue,
  withTiming,
} from "react-native-reanimated";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useStyles, useTheme } from "../lib/appearance";
import { layoutMessageMenu, MENU_ROW_HEIGHT, type Frame } from "../lib/message-menu";
import { font, Palette, radius, size, space } from "../lib/theme";

export interface MessageMenuItem {
  key: string;
  label: string;
  icon: ComponentProps<typeof Feather>["name"];
  onPress(): void;
}

/** How far a held message grows. Enough to read as picked up, not zoomed. */
const LIFT = 1.03;
const OPEN_MS = 220;
const CLOSE_MS = 150;

/**
 * A held message, lifted out of the conversation with its actions beside it.
 *
 * This is how holding a message works in Messages, Telegram, WhatsApp, X and
 * the rest, and it is the pattern people already know: the conversation blurs
 * back, the message comes forward exactly where it was, and a short menu
 * attaches to it. It says which message you are acting on without asking you
 * to read anything, and putting it away is tapping anywhere else.
 *
 * The first version of this copied silently on a hold and printed "Copied"
 * under the bubble. It worked, and nobody could tell it was going to until it
 * had — there was nothing to see, nothing to choose, and no way to hold a
 * message without copying it.
 *
 * Open while `anchor` is set, which is the message's frame on screen. The
 * menu animates itself out before calling onClose, so the caller only ever
 * sets the anchor and clears it on onClose.
 */
export function MessageMenu({
  anchor,
  items,
  onClose,
  bubbleStyle,
  children,
  align = "right",
}: {
  anchor: Frame | null;
  items: MessageMenuItem[];
  onClose(): void;
  /** The message's own look — the lifted copy has to match what it replaced. */
  bubbleStyle: StyleProp<ViewStyle>;
  /** The message's content, drawn again in the lifted copy. */
  children: ReactNode;
  align?: "left" | "right";
}) {
  const styles = useStyles(makeStyles);
  const { color, scheme } = useTheme();
  const insets = useSafeAreaInsets();
  const screen = useWindowDimensions();
  const reduceMotion = useReducedMotion();
  const progress = useSharedValue(0);

  useEffect(() => {
    if (!anchor) return;
    progress.value = 0;
    progress.value = withTiming(1, {
      duration: reduceMotion ? 0 : OPEN_MS,
      easing: Easing.out(Easing.cubic),
    });
  }, [anchor, progress, reduceMotion]);

  // Closing is three steps: animate out, take the Modal down, then hand
  // control back and run the chosen action. The action waits for the Modal to
  // be gone because iOS presents a share sheet from whatever is on top — a
  // Modal still on its way out takes the sheet down with it. iOS says when the
  // Modal has gone (onDismiss); Android does not, and needs no wait, since
  // its share sheet is a separate activity.
  const [leaving, setLeaving] = useState(false);
  const closing = useRef(false);
  const after = useRef<(() => void) | undefined>(undefined);

  const finish = useCallback(() => {
    const action = after.current;
    after.current = undefined;
    closing.current = false;
    setLeaving(false);
    onClose();
    action?.();
  }, [onClose]);

  const close = useCallback(
    (action?: () => void) => {
      // A second tap while the first is animating out is the same close, and
      // must not replace the action the first one chose.
      if (closing.current) return;
      closing.current = true;
      after.current = action;
      const hide = () => (Platform.OS === "ios" ? setLeaving(true) : finish());
      progress.value = withTiming(
        0,
        { duration: reduceMotion ? 0 : CLOSE_MS, easing: Easing.in(Easing.quad) },
        (done) => {
          if (done) runOnJS(hide)();
        },
      );
    },
    [finish, progress, reduceMotion],
  );

  const backdrop = useAnimatedStyle(() => ({ opacity: progress.value }));
  const lifted = useAnimatedStyle(() => ({
    transform: [{ scale: interpolate(progress.value, [0, 1], [1, reduceMotion ? 1 : LIFT]) }],
  }));
  const menuMotion = useAnimatedStyle(() => ({
    opacity: progress.value,
    transform: [{ scale: interpolate(progress.value, [0, 1], [reduceMotion ? 1 : 0.9, 1]) }],
  }));

  if (!anchor) return null;

  const layout = layoutMessageMenu({ anchor, rows: items.length, screen, insets, align });
  // The menu grows out of the corner nearest the message, so it reads as
  // coming from it rather than appearing beside it.
  const origin = `${layout.placement === "below" ? "top" : "bottom"} ${align}`;

  return (
    <Modal
      visible={!leaving}
      transparent
      animationType="none"
      statusBarTranslucent
      onRequestClose={() => close()}
      onDismiss={finish}
    >
      <Animated.View style={[StyleSheet.absoluteFill, backdrop]}>
        {/* Blur where the platform does it properly; a plain dim elsewhere,
            since Android's blur is still experimental. The dim sits on top
            either way so the lifted message has something to stand out
            from in light mode, where a blur alone is nearly white. */}
        {Platform.OS === "ios" ? (
          <BlurView
            intensity={28}
            tint={scheme === "dark" ? "dark" : "light"}
            style={StyleSheet.absoluteFill}
          />
        ) : null}
        <View style={[StyleSheet.absoluteFill, styles.dim]} />
      </Animated.View>

      {/* Anywhere that is not the message or the menu puts it away. */}
      <Pressable
        style={StyleSheet.absoluteFill}
        onPress={() => close()}
        accessibilityRole="button"
        accessibilityLabel="Close menu"
      />

      <Animated.View
        pointerEvents="none"
        style={[
          bubbleStyle,
          styles.lifted,
          {
            position: "absolute",
            left: layout.bubble.x,
            top: layout.bubble.y,
            width: layout.bubble.width,
            height: layout.bubble.height,
            // alignSelf and maxWidth belong to the message in the list; here
            // it has an exact frame.
            alignSelf: "auto",
            maxWidth: undefined,
          },
          layout.clipped && styles.clipped,
          lifted,
        ]}
      >
        {children}
      </Animated.View>

      <Animated.View
        accessibilityViewIsModal
        style={[
          styles.menu,
          {
            left: layout.menu.x,
            top: layout.menu.y,
            width: layout.menu.width,
            transformOrigin: origin,
          },
          menuMotion,
        ]}
      >
        {items.map((item, index) => (
          <Pressable
            key={item.key}
            onPress={() => close(item.onPress)}
            style={({ pressed }) => [
              styles.row,
              index > 0 && styles.rowDivided,
              pressed && styles.rowPressed,
            ]}
            accessibilityRole="menuitem"
            accessibilityLabel={item.label}
          >
            <Feather name={item.icon} size={17} color={color.text} />
            <Text style={styles.label}>{item.label}</Text>
          </Pressable>
        ))}
      </Animated.View>
    </Modal>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    // The system's own scrim, at half strength over a blur. At full strength
    // it is tuned to stand alone, and over a blur it went muddy.
    dim: { backgroundColor: c.scrim, opacity: Platform.OS === "ios" ? 0.5 : 1 },
    lifted: {
      shadowColor: c.shadow,
      shadowOpacity: 0.22,
      shadowRadius: 24,
      shadowOffset: { width: 0, height: 12 },
      elevation: 12,
      // A shadow disappears on a dark ground; a hairline does not.
      borderWidth: StyleSheet.hairlineWidth,
      borderColor: c.line,
    },
    clipped: { overflow: "hidden" },
    menu: {
      position: "absolute",
      borderRadius: radius.lg,
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
      overflow: "hidden",
      shadowColor: c.shadow,
      shadowOpacity: 0.18,
      shadowRadius: 24,
      shadowOffset: { width: 0, height: 10 },
      elevation: 16,
    },
    row: {
      height: MENU_ROW_HEIGHT,
      flexDirection: "row",
      alignItems: "center",
      gap: space.md,
      paddingHorizontal: space.lg,
    },
    rowDivided: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: c.line },
    rowPressed: { backgroundColor: c.fill },
    label: { flex: 1, fontFamily: font.sansMedium, fontSize: size.body, color: c.text },
  });
