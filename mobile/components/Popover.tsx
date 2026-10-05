import Feather from "@expo/vector-icons/Feather";
import { ComponentProps } from "react";
import {
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  useWindowDimensions,
} from "react-native";
import Animated, {
  Easing,
  FadeIn,
  FadeOut,
  useReducedMotion,
} from "react-native-reanimated";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useStyles, useTheme } from "../lib/appearance";
import { font, Palette, radius, size, space } from "../lib/theme";

export interface PopoverItem {
  key: string;
  label: string;
  /** Absent for a list of choices, where a mark per row would say nothing. */
  icon?: ComponentProps<typeof Feather>["name"];
  /** The choice in effect now, marked with a check. */
  checked?: boolean;
  /** Machine text — a mode or model id — set in mono, as everywhere else. */
  mono?: boolean;
  /** Shown right-aligned, for a count or a short state. */
  detail?: string;
  destructive?: boolean;
  disabled?: boolean;
  onPress(): void;
}

/**
 * A menu anchored to the control that opened it.
 *
 * This replaces an iOS action sheet, which lays two options out side by side
 * and puts Cancel first — so a session with only one action available showed
 * "[Cancel] [Servers (2)]" in a row, reading backwards and looking broken. A
 * sheet also arrives from the bottom of the screen, which says nothing about
 * what it belongs to.
 *
 * A popover under the control it came from has neither problem: one item or
 * four, it is the same object in the same place, and dismissing it is tapping
 * away rather than finding a button.
 */
export function Popover({
  visible,
  onDismiss,
  items,
  /** Distance from the top of the screen, below whatever opened it. */
  top,
  /** Which edge it hangs from: the side the control that opened it is on. */
  align = "right",
  /** A line above the items saying what choosing one does. */
  title,
}: {
  visible: boolean;
  onDismiss: () => void;
  items: PopoverItem[];
  top: number;
  align?: "left" | "right";
  title?: string;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const insets = useSafeAreaInsets();
  const window = useWindowDimensions();
  const reduceMotion = useReducedMotion();
  // A list of models can be longer than the screen; it scrolls inside the
  // card rather than running off the bottom.
  const maxHeight = Math.max(160, window.height - insets.top - top - insets.bottom - space.xl);

  if (items.length === 0) return null;

  return (
    <Modal visible={visible} transparent animationType="none" onRequestClose={onDismiss}>
      {/* The backdrop is the dismiss target, so putting the menu away is the
          same gesture as losing interest in it. */}
      <Pressable style={styles.backdrop} onPress={onDismiss} accessibilityLabel="Close menu" />
      <Animated.View
        entering={reduceMotion ? undefined : FadeIn.duration(120).easing(Easing.out(Easing.quad))}
        exiting={reduceMotion ? undefined : FadeOut.duration(90)}
        style={[
          styles.card,
          align === "left" ? styles.cardLeft : styles.cardRight,
          { top: insets.top + top, maxHeight, maxWidth: window.width - space.lg * 2 },
        ]}
      >
        <ScrollView style={styles.scroll} bounces={false} accessibilityRole="menu">
          {title ? <Text style={styles.title}>{title}</Text> : null}
          {items.map((item, index) => (
            <Pressable
              key={item.key}
              onPress={() => {
                onDismiss();
                item.onPress();
              }}
              disabled={item.disabled}
              style={({ pressed }) => [
                styles.row,
                (index > 0 || title) && styles.rowDivided,
                pressed && styles.rowPressed,
                item.disabled && styles.rowDisabled,
              ]}
              accessibilityRole="menuitem"
              accessibilityLabel={item.detail ? `${item.label}, ${item.detail}` : item.label}
              accessibilityState={{ disabled: item.disabled, selected: item.checked }}
            >
              {item.icon ? (
                <Feather
                  name={item.icon}
                  size={16}
                  color={item.destructive ? color.errorText : color.muted}
                />
              ) : null}
              <Text
                style={[
                  styles.label,
                  item.mono && styles.labelMono,
                  item.destructive && styles.labelDestructive,
                ]}
                numberOfLines={1}
              >
                {item.label}
              </Text>
              {item.detail ? <Text style={styles.detail}>{item.detail}</Text> : null}
              {item.checked ? <Feather name="check" size={16} color={color.working} /> : null}
            </Pressable>
          ))}
        </ScrollView>
      </Animated.View>
    </Modal>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    backdrop: { flex: 1, backgroundColor: c.scrim },
    card: {
      position: "absolute",
      minWidth: 212,
      borderRadius: radius.lg,
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
      overflow: "hidden",
      shadowColor: c.shadow,
      shadowOpacity: 0.18,
      shadowRadius: 24,
      shadowOffset: { width: 0, height: 10 },
      elevation: 12,
    },
    cardLeft: { left: space.lg },
    // Sized to its rows, and shrunk by the card's maxHeight only when they
    // do not fit; a growing ScrollView would always fill the cap.
    scroll: { flexGrow: 0 },
    cardRight: { right: space.lg },
    title: {
      fontFamily: font.sans,
      fontSize: size.label,
      lineHeight: 16,
      color: c.muted,
      paddingHorizontal: space.lg,
      paddingTop: space.md,
      paddingBottom: space.sm,
    },
    row: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.md,
      minHeight: 46,
      paddingHorizontal: space.lg,
    },
    // Hairlines between rows rather than around each: it is one object.
    rowDivided: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: c.line },
    rowPressed: { backgroundColor: c.fill },
    rowDisabled: { opacity: 0.4 },
    label: { flex: 1, fontFamily: font.sansMedium, fontSize: size.caption, color: c.text },
    labelMono: { fontFamily: font.monoMedium, fontSize: 12.5 },
    labelDestructive: { color: c.errorText },
    detail: { fontFamily: font.monoMedium, fontSize: size.label, color: c.faint },
  });
