import { Image } from "expo-image";
import { useId, useState, type ReactNode } from "react";
import {
  Image as RNImage,
  StyleSheet,
  View,
  type ImageStyle,
  type StyleProp,
  type TextStyle,
  type ViewStyle,
} from "react-native";
import { useReducedMotion } from "react-native-reanimated";
import Svg, {
  Circle,
  Defs,
  Mask,
  Path,
  Pattern,
  RadialGradient,
  Rect,
  Stop,
  Text as SvgText,
} from "react-native-svg";

import { useTheme } from "../lib/appearance";
import { font, size, type Palette } from "../lib/theme";

/**
 * The website's look, for the screens where nobody is working: first run,
 * pairing and empty lists. An orange flood with a film grain and a wavy edge,
 * Rubik titles with a word outlined, cream cards with an ink edge and a hard
 * shadow, a patch of dots, and the site's stickers playing. Threads, files
 * and settings never use these.
 */

/** The site's hard shadow: a solid edge colour straight below, no blur. */
export function hardShadow(color: string, depth = 3): ViewStyle {
  return { boxShadow: `0px ${depth}px 0px ${color}` };
}

/**
 * The site's look as styles every screen shares. Things you tap or that hold
 * something get the ink edge; the ones that stand out also get the hard
 * shadow under it. Messages, code and diffs never do.
 */
export function edged(c: Palette, depth = 0): ViewStyle {
  return { borderWidth: 1.5, borderColor: c.edge, ...(depth > 0 ? hardShadow(c.edge, depth) : null) };
}

/** A screen's big title, in Rubik as on the site. */
export function screenTitle(c: Palette): TextStyle {
  return { ...font.display, fontSize: size.display, letterSpacing: -size.display * 0.03, color: c.text };
}

/** A heading in a screen or a sheet, in Rubik. */
export function heading(c: Palette, points: number = size.title): TextStyle {
  return { ...font.displayBold, fontSize: points, letterSpacing: -points * 0.01, color: c.text };
}

/** The site's small labels: mono, upper case, spaced out. */
export function eyebrow(c: Palette): TextStyle {
  return { ...font.monoMedium, fontSize: size.label, letterSpacing: 1.3, textTransform: "uppercase", color: c.muted };
}

// One wave of the site's edge, 22 wide and 10 tall, filled below the curve.
const WAVE = "M0 5Q5.5 0 11 5T22 5V10H0Z";

/** The wavy edge the site puts between bands, in the colour of what comes next. */
export function WaveEdge({ color, style }: { color: string; style?: StyleProp<ViewStyle> }) {
  const id = `wave${useId().replace(/[^a-zA-Z0-9]/g, "")}`;
  return (
    <View pointerEvents="none" style={[styles.wave, style]}>
      <Svg width="100%" height={10}>
        <Defs>
          <Pattern id={id} width={22} height={10} patternUnits="userSpaceOnUse">
            <Path d={WAVE} fill={color} />
          </Pattern>
        </Defs>
        <Rect width="100%" height={10} fill={`url(#${id})`} />
      </Svg>
    </View>
  );
}

/**
 * The orange band: grain over the flood, and a wavy edge at the foot in the
 * page's colour. The flood also reaches up past the top, so pulling a
 * scrolled screen down shows more orange rather than the page.
 */
export function FloodBand({ children, style }: { children: ReactNode; style?: StyleProp<ViewStyle> }) {
  const { color } = useTheme();
  return (
    <View style={[{ backgroundColor: color.flood }, style]}>
      <View pointerEvents="none" style={[styles.above, { backgroundColor: color.flood }]} />
      <RNImage
        source={require("../assets/brand/grain.png")}
        resizeMode="repeat"
        style={styles.grain}
        accessibilityIgnoresInvertColors
      />
      {children}
      <WaveEdge color={color.paper} />
    </View>
  );
}

/**
 * One line of Rubik drawn as an outline, the way the site sets a word in its
 * headlines. Screen readers skip it: the title around it carries the words.
 */
export function OutlinedText({
  text,
  fontSize,
  color,
  strokeWidth = 1.8,
}: {
  text: string;
  fontSize: number;
  color: string;
  strokeWidth?: number;
}) {
  const [width, setWidth] = useState(0);
  const height = Math.ceil(fontSize * 1.04);
  return (
    <View
      style={{ height, alignSelf: "stretch" }}
      onLayout={(e) => setWidth(e.nativeEvent.layout.width)}
      accessibilityElementsHidden
      importantForAccessibility="no-hide-descendants"
    >
      {width > 0 ? (
        <Svg width={width} height={height}>
          <SvgText
            x={strokeWidth}
            y={fontSize * 0.84}
            fontFamily="Rubik-Bold"
            fontSize={fontSize}
            letterSpacing={-fontSize * 0.03}
            fill="none"
            stroke={color}
            strokeWidth={strokeWidth}
            strokeLinejoin="round"
          >
            {text}
          </SvgText>
        </Svg>
      ) : null}
    </View>
  );
}

/** A patch of the site's dots, fading out from its middle, behind a sticker. */
export function DotPatch({ size, spacing = 16, style }: { size: number; spacing?: number; style?: StyleProp<ViewStyle> }) {
  const { color } = useTheme();
  const id = useId().replace(/[^a-zA-Z0-9]/g, "");
  return (
    <View pointerEvents="none" style={[{ width: size, height: size }, style]}>
      <Svg width={size} height={size}>
        <Defs>
          <Pattern id={`dots${id}`} width={spacing} height={spacing} patternUnits="userSpaceOnUse">
            <Circle cx={spacing / 2} cy={spacing / 2} r={1.5} fill={color.creamDots} />
          </Pattern>
          <RadialGradient id={`fade${id}`} cx="50%" cy="50%" r="50%">
            <Stop offset="0.45" stopColor="#fff" stopOpacity={1} />
            <Stop offset="1" stopColor="#fff" stopOpacity={0} />
          </RadialGradient>
          <Mask id={`mask${id}`}>
            <Rect width={size} height={size} fill={`url(#fade${id})`} />
          </Mask>
        </Defs>
        <Rect width={size} height={size} fill={`url(#dots${id})`} mask={`url(#mask${id})`} />
      </Svg>
    </View>
  );
}

/**
 * The cream cards' dots, filling the card behind its content. The card keeps
 * its own overflow so its hard shadow shows; this clips to its corners.
 */
export function DotFill({ radius, spacing = 16 }: { radius: number; spacing?: number }) {
  const { color } = useTheme();
  const id = `fill${useId().replace(/[^a-zA-Z0-9]/g, "")}`;
  return (
    <View pointerEvents="none" style={[StyleSheet.absoluteFill, { borderRadius: radius, overflow: "hidden" }]}>
      <Svg width="100%" height="100%">
        <Defs>
          <Pattern id={id} width={spacing} height={spacing} patternUnits="userSpaceOnUse">
            <Circle cx={spacing / 2} cy={spacing / 2} r={1.2} fill={color.creamDots} />
          </Pattern>
        </Defs>
        <Rect width="100%" height="100%" fill={`url(#${id})`} />
      </Svg>
    </View>
  );
}

/**
 * The site's stickers, exported from brand/mascot with fun-isometric as
 * looping animated WebP, and a still for Reduce Motion.
 */
const STICKERS = {
  phone: { moving: require("../assets/stickers/phone.webp"), still: require("../assets/stickers/phone.png") },
  terminal: { moving: require("../assets/stickers/terminal.webp"), still: require("../assets/stickers/terminal.png") },
  heart: { moving: require("../assets/stickers/heart.webp"), still: require("../assets/stickers/heart.png") },
  notes: { moving: require("../assets/stickers/notes.webp"), still: require("../assets/stickers/notes.png") },
  mascot: { moving: require("../assets/stickers/mascot.webp"), still: require("../assets/stickers/mascot.png") },
} as const;

export type StickerName = keyof typeof STICKERS;

export function Sticker({ name, size, style }: { name: StickerName; size: number; style?: StyleProp<ImageStyle> }) {
  const reduceMotion = useReducedMotion();
  const sticker = STICKERS[name];
  return (
    <Image
      source={reduceMotion ? sticker.still : sticker.moving}
      style={[{ width: size, height: size }, style]}
      contentFit="contain"
      autoplay
      accessible={false}
    />
  );
}

/**
 * A sticker on its patch of dots, as the empty screens show it. On a dark
 * page it sits on a cream disc: its black outline would vanish on black.
 */
export function StickerOnDots({ name, size }: { name: StickerName; size: number }) {
  const { color } = useTheme();
  const patch = Math.round(size * 1.14);
  const disc = Math.round(size * 0.84);
  return (
    <View style={{ width: patch, height: patch, alignItems: "center", justifyContent: "center" }}>
      <DotPatch size={patch} style={StyleSheet.absoluteFill} />
      {color.stickerSpot !== "transparent" ? (
        <View
          pointerEvents="none"
          style={{ position: "absolute", width: disc, height: disc, borderRadius: disc / 2, backgroundColor: color.stickerSpot }}
        />
      ) : null}
      <Sticker name={name} size={size} />
    </View>
  );
}

const styles = StyleSheet.create({
  wave: { position: "absolute", left: 0, right: 0, bottom: -1, height: 10 },
  above: { position: "absolute", left: 0, right: 0, top: -1000, height: 1000 },
  grain: {
    position: "absolute",
    left: 0,
    right: 0,
    bottom: 0,
    top: -1000,
    opacity: 0.2,
    mixBlendMode: "overlay",
  },
});
