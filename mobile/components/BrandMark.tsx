import { StyleSheet, Text, View } from "react-native";
import Svg, { Path } from "react-native-svg";

import { useStyles, useTheme } from "../lib/appearance";
import { font, Palette } from "../lib/theme";

/**
 * The mark: a terminal prompt with a notification waiting on it, which is the
 * whole product in one shape. Drawn rather than bundled as an image, so it is
 * crisp at any size and takes the theme's ink.
 */
export function BrandMark({
  size = 26,
  ring,
}: {
  size?: number;
  /** The colour behind the mark, so the badge's ring cuts out cleanly. */
  ring?: string;
}) {
  const { color } = useTheme();
  const badge = Math.round(size * 0.42);
  const ringWidth = Math.max(2, Math.round(size * 0.09));
  return (
    <View
      style={{
        width: size,
        height: size,
        borderRadius: Math.round(size * 0.3),
        backgroundColor: color.inverse,
      }}
      accessible={false}
    >
      <Svg width={size} height={size} viewBox="0 0 100 100">
        <Path
          d="M31 32.5 L55 51.25 L31 70"
          fill="none"
          stroke={color.onInverse}
          strokeWidth={12}
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </Svg>
      <View
        style={{
          position: "absolute",
          right: -Math.round(badge * 0.36),
          top: -Math.round(badge * 0.36),
          width: badge,
          height: badge,
          borderRadius: badge / 2,
          backgroundColor: color.needsYou,
          borderWidth: ringWidth,
          borderColor: ring ?? color.paper,
        }}
      />
    </View>
  );
}

/** Mark and name together, as the app's header lockup. */
export function Wordmark({ size = 22 }: { size?: number }) {
  const styles = useStyles(makeStyles);
  return (
    <View style={styles.row}>
      <BrandMark size={size} />
      <Text style={styles.name}>agentman</Text>
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    row: { flexDirection: "row", alignItems: "center", gap: 10 },
    name: { fontFamily: font.sansBold, fontSize: 18, letterSpacing: -0.4, color: c.text },
  });
