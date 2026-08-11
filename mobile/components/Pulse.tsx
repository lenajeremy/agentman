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

import { color } from "../lib/theme";

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
export function Pulse({ state, size = 8 }: { state: string; size?: number }) {
  const scale = useSharedValue(1);
  const opacity = useSharedValue(0.5);
  const reduceMotion = useReducedMotion();

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
        width: size * 3,
        height: size * 3,
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
          opacity: state === "idle" ? 0.45 : 1,
        }}
      />
    </View>
  );
}
