import { useEffect } from "react";
import { View } from "react-native";
import Animated, {
  Easing,
  cancelAnimation,
  useAnimatedStyle,
  useReducedMotion,
  useSharedValue,
  withRepeat,
  withTiming,
} from "react-native-reanimated";

import { useTheme } from "../lib/appearance";

/**
 * The state indicator, and the only persistent motion on the status board.
 *
 * A working agent breathes; an idle one sits still. That is the whole signal —
 * it is legible from across a room, which is the point of a status board, and
 * it costs nothing to read because nothing else on the screen moves.
 *
 * Everything here is deliberately restrained: no spinners, no progress bars,
 * no shimmer. Motion means "something is happening right now", so anything
 * else that moved would dilute it.
 */
export function Pulse({
  state,
  size = 8,
  inline = false,
}: {
  state: string;
  size?: number;
  /**
   * Occupy only the dot, not the room its halo sweeps through.
   *
   * The halo is absolutely positioned, so it still draws outside these bounds
   * — nothing clips it. What changes is layout: on a line of text the reserved
   * box put twenty-three points of nothing before the dot, which read as the
   * dot having been indented away from the line it belongs to.
   */
  inline?: boolean;
}) {
  const scale = useSharedValue(1);
  const opacity = useSharedValue(0.5);
  const reduceMotion = useReducedMotion();
  const { color } = useTheme();

  const isWorking = state === "busy";

  useEffect(() => {
    if (!isWorking || reduceMotion) {
      cancelAnimation(scale);
      cancelAnimation(opacity);
      scale.value = withTiming(1, { duration: 150 });
      opacity.value = withTiming(isWorking ? 0.9 : 0.5, { duration: 150 });
      return;
    }
    // Slow enough to read as breathing rather than blinking. A blink reads as
    // an alarm, and "working" is not an alarm.
    scale.value = withRepeat(
      withTiming(2.6, { duration: 1600, easing: Easing.linear }),
      -1,
      false,
    );
    opacity.value = withRepeat(
      withTiming(0, { duration: 1600, easing: Easing.linear }),
      -1,
      false,
    );
    return () => {
      cancelAnimation(scale);
      cancelAnimation(opacity);
    };
  }, [isWorking, reduceMotion, scale, opacity]);

  const halo = useAnimatedStyle(() => ({
    transform: [{ scale: scale.value }],
    opacity: opacity.value,
  }));

  const dotColor =
    state === "waiting_input"
      ? color.needsYou
      : state === "busy"
        ? color.working
        : color.faint;

  return (
    <View
      style={{
        width: inline ? size : size * 3,
        height: inline ? size : size * 3,
        alignItems: "center",
        justifyContent: "center",
      }}
    >
      {isWorking && !reduceMotion && (
        <Animated.View
          style={[
            {
              position: "absolute",
              width: size,
              height: size,
              borderRadius: size / 2,
              backgroundColor: dotColor,
            },
            halo,
          ]}
        />
      )}
      <View
        style={{
          width: size,
          height: size,
          borderRadius: size / 2,
          backgroundColor: dotColor,
          // Idle needs to read as "nothing is happening" without disappearing.
          opacity: state === "idle" ? 0.6 : 1,
        }}
      />
    </View>
  );
}
