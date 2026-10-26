import { View } from "react-native";

/**
 * A fade behind the chrome over a photo.
 *
 * Six bands rather than a real gradient: expo-linear-gradient is a native
 * module, and pulling one in — with the build cycle that costs — to darken a
 * strip is not a trade worth making. The flat wash it replaces lost badly over
 * a light screenshot, where the filename landed in the middle of the picture's
 * own text and neither could be read.
 */
/** Reaches past the bar it sits behind, so the fade lands below the text. */
const scrimFill = {
  position: "absolute",
  top: 0,
  left: 0,
  right: 0,
  bottom: -56,
} as const;
/** The same fade for the bar at the foot, reaching up instead. */
const scrimFillBottom = { ...scrimFill, top: -56, bottom: 0 } as const;
const scrimBands = [0.92, 0.92, 0.92, 0.9, 0.84, 0.66, 0.4, 0.18, 0.05];

export function PhotoScrim({ edge = "top" }: { edge?: "top" | "bottom" }) {
  // Darkest against the screen edge either way, fading toward the picture.
  const bands = edge === "top" ? scrimBands : [...scrimBands].reverse();
  return (
    <View style={edge === "top" ? scrimFill : scrimFillBottom} pointerEvents="none">
      {bands.map((opacity, index) => (
        <View
          key={index}
          style={{ flex: 1, backgroundColor: `rgba(0, 0, 0, ${opacity})` }}
        />
      ))}
    </View>
  );
}
