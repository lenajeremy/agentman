import Feather from "@expo/vector-icons/Feather";
import { ScrollView, StyleSheet, Text, View } from "react-native";

import { useStyles, useTheme } from "../lib/appearance";
import { hostName, type Host } from "../lib/fleet";
import { font, Palette, radius, size, space } from "../lib/theme";
import { edged } from "./Brand";
import { MotionPressable } from "./MotionPressable";

export const ALL_DEVICES = "all";

/**
 * Horizontal computer picker for screens that span or target paired devices
 * (Usage, Notifications, Agentman settings). Hidden when only one computer is
 * paired so single-device layouts keep zero extra chrome.
 */
export function DevicePicker({
  hosts,
  value,
  onChange,
  includeAll = false,
  allLabel = "All devices",
  compact = false,
}: {
  hosts: readonly Host[];
  value: string;
  onChange(next: string): void;
  includeAll?: boolean;
  allLabel?: string;
  /** Keep flush inside a padded column instead of bleeding edge-to-edge. */
  compact?: boolean;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  if (hosts.length < 2) return null;

  return (
    <ScrollView
      horizontal
      showsHorizontalScrollIndicator={false}
      style={compact ? styles.barCompact : styles.bar}
      contentContainerStyle={compact ? styles.rowCompact : styles.row}
    >
      {includeAll ? (
        <MotionPressable
          onPress={() => onChange(ALL_DEVICES)}
          style={[styles.chip, value === ALL_DEVICES && styles.chipOn]}
          pressedScale={0.96}
          accessibilityRole="button"
          accessibilityState={{ selected: value === ALL_DEVICES }}
        >
          <Feather
            name="layers"
            size={13}
            color={value === ALL_DEVICES ? color.onInverse : color.muted}
          />
          <Text
            style={[styles.label, value === ALL_DEVICES && styles.labelOn]}
            numberOfLines={1}
          >
            {allLabel}
          </Text>
        </MotionPressable>
      ) : null}
      {hosts.map((host, index) => {
        const name = hostName(host, index);
        const selected = value === host.id;
        const online = host.store.daemonOnline;
        return (
          <MotionPressable
            key={host.id}
            onPress={() => onChange(host.id)}
            style={[styles.chip, selected && styles.chipOn]}
            pressedScale={0.96}
            accessibilityRole="button"
            accessibilityState={{ selected }}
            accessibilityLabel={`${name}, ${online ? "online" : "offline"}`}
          >
            <View
              style={[
                styles.dot,
                {
                  backgroundColor: selected
                    ? online
                      ? color.ok
                      : color.onInverse
                    : online
                      ? color.ok
                      : color.faint,
                  opacity: selected && !online ? 0.45 : 1,
                },
              ]}
            />
            <Text
              style={[styles.label, selected && styles.labelOn]}
              numberOfLines={1}
            >
              {name}
            </Text>
          </MotionPressable>
        );
      })}
    </ScrollView>
  );
}

const makeSiteStyles = (c: Palette) =>
  StyleSheet.create({
    bar: { marginTop: space.md, marginHorizontal: -space.lg },
    barCompact: { marginBottom: space.sm },
    row: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      paddingHorizontal: space.lg,
      paddingVertical: 2,
    },
    rowCompact: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      paddingVertical: 2,
    },
    chip: {
      flexDirection: "row",
      alignItems: "center",
      gap: 6,
      minHeight: 36,
      maxWidth: 200,
      paddingHorizontal: space.md,
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      ...edged(c),
    },
    chipOn: { backgroundColor: c.inverse },
    dot: { width: 6, height: 6, borderRadius: 3, flexShrink: 0 },
    label: { flexShrink: 1, ...font.sansMedium, fontSize: size.caption, color: c.textSecondary },
    labelOn: { color: c.onInverse },
  });

const makeClassicStyles = (c: Palette) =>
  StyleSheet.create({
    bar: { marginTop: space.md, marginHorizontal: -space.lg },
    barCompact: { marginBottom: space.sm },
    row: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      paddingHorizontal: space.lg,
      paddingVertical: 2,
    },
    rowCompact: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      paddingVertical: 2,
    },
    chip: {
      flexDirection: "row",
      alignItems: "center",
      gap: 6,
      minHeight: 36,
      maxWidth: 200,
      paddingHorizontal: space.md,
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
    },
    chipOn: { backgroundColor: c.inverse, borderColor: c.inverse },
    dot: { width: 6, height: 6, borderRadius: 3, flexShrink: 0 },
    label: { flexShrink: 1, ...font.sansMedium, fontSize: size.caption, color: c.textSecondary },
    labelOn: { color: c.onInverse },
  });

type Styles = ReturnType<typeof makeSiteStyles> & ReturnType<typeof makeClassicStyles>;
const makeStyles = (c: Palette): Styles =>
  (c.look === "site" ? makeSiteStyles(c) : { ...makeSiteStyles(c), ...makeClassicStyles(c) }) as Styles;
