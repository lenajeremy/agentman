import { useEffect } from "react";
import { View } from "react-native";
import Animated, {
  cancelAnimation,
  Easing,
  interpolateColor,
  useAnimatedProps,
  useAnimatedStyle,
  useReducedMotion,
  useSharedValue,
  withRepeat,
  withTiming,
} from "react-native-reanimated";
import Svg, { Circle } from "react-native-svg";

import { useTheme } from "../lib/appearance";

const AnimatedCircle = Animated.createAnimatedComponent(Circle);

/** Seconds each colour has before the next has taken over. */
const HOLD = 1.2;
/** How much of a turn from one colour to the next the ring spans at once. */
const SPREAD = 0.35;
/** The part of its turn a colour rests before the next comes in. */
const REST = 0.6;
/** The tail starts a little in, so its round cap stays in the fade. */
const START = 0.04;
/** Pieces the ring is drawn in, tail to head, each with its own colour. */
const PIECES = 12;
/** One loop of the clock; every motion below repeats within it. */
const LOOP = 43.2;

/**
 * What an agent shows while it works, the Mac's own (desktop/Agentman/
 * Design/Loader.swift): a ring chasing round, its tail fading and its length
 * breathing, its colours flowing along it. Each colour rests on the whole
 * ring a moment, then the next comes in at the head and spreads back to the
 * tail. Still when the phone is asked to reduce motion.
 *
 * SVG has no angular gradient, so the ring is drawn in pieces from tail to
 * head, each a step further along in colour and opacity.
 */
export function Loader({ size = 11 }: { size?: number }) {
  const { color } = useTheme();
  const reduceMotion = useReducedMotion();
  // Seconds, looping over a span every motion divides evenly: 1.44 s of
  // breathing, 0.9 s per turn, and 2.4 s per pair of colours.
  const time = useSharedValue(0);

  useEffect(() => {
    if (reduceMotion) {
      cancelAnimation(time);
      time.value = 0.6;
      return;
    }
    time.value = 0;
    time.value = withRepeat(withTiming(LOOP, { duration: LOOP * 1000, easing: Easing.linear }), -1, false);
    return () => cancelAnimation(time);
  }, [reduceMotion, time]);

  const stroke = Math.max(1.6, size * 0.18);
  const r = (size - stroke) / 2;
  const around = 2 * Math.PI * r;
  const colors = [color.working, color.loaderPink];

  const spin = useAnimatedStyle(() => ({
    transform: [{ rotate: `${(time.value * 400) % 360}deg` }],
  }));

  return (
    <View style={{ width: size, height: size }} accessible accessibilityRole="progressbar" accessibilityLabel="Working">
      <Animated.View style={[{ width: size, height: size }, spin]}>
        <Svg width={size} height={size}>
          {Array.from({ length: PIECES }, (_, index) => (
            <Piece
              key={index}
              index={index}
              time={time}
              size={size}
              r={r}
              around={around}
              stroke={stroke}
              colors={colors}
            />
          ))}
        </Svg>
      </Animated.View>
    </View>
  );
}

function Piece({
  index,
  time,
  size,
  r,
  around,
  stroke,
  colors,
}: {
  index: number;
  time: { value: number };
  size: number;
  r: number;
  around: number;
  stroke: number;
  colors: string[];
}) {
  const head = index === PIECES - 1;
  const props = useAnimatedProps(() => {
    const t = time.value;
    const end = START + 0.5 + 0.22 * Math.sin((t * Math.PI * 2) / 1.44);
    const length = (end - START) / PIECES;
    const from = START + length * index;
    // The middle of the piece, from tail (0) to head (1).
    const along = (index + 0.5) / PIECES;
    const phase = t / HOLD + along * SPREAD;
    const step = Math.floor(phase);
    const x = Math.min(1, Math.max(0, (phase - step - REST) / (1 - REST)));
    const eased = x * x * (3 - 2 * x);
    const first = step % 2 === 0;
    const mixed = interpolateColor(eased, [0, 1], first ? [colors[0], colors[1]] : [colors[1], colors[0]]);
    // A hair longer than its share, so neighbours meet without a seam.
    const drawn = (length + (head ? 0 : 0.01)) * around;
    return {
      stroke: mixed,
      strokeOpacity: Math.min(1, along / 0.25),
      strokeDasharray: [drawn, around],
      strokeDashoffset: -from * around,
    };
  });

  return (
    <AnimatedCircle
      cx={size / 2}
      cy={size / 2}
      r={r}
      fill="none"
      strokeWidth={stroke}
      strokeLinecap={head ? "round" : "butt"}
      animatedProps={props}
    />
  );
}
