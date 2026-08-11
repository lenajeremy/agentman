import { useEffect, useRef } from "react";
import { StyleProp, ViewStyle } from "react-native";
import Animated, {
  Easing,
  useAnimatedStyle,
  useReducedMotion,
  useSharedValue,
  withDelay,
  withTiming,
} from "react-native-reanimated";

/**
 * Fades and lifts content the first time it appears.
 *
 * Live output otherwise materializes instantly, which makes it easy to miss
 * that anything changed — the feed just looks different than it did a second
 * ago. A short rise gives the eye something to follow to the new row.
 *
 * Callers decide what is genuinely new. Session history, for example, renders
 * settled so opening a transcript is not a cascade of fades.
 */
export function Appear({
  children,
  delay = 0,
  enabled = true,
  offset = 6,
  style: containerStyle,
}: {
  children: React.ReactNode;
  delay?: number;
  enabled?: boolean;
  offset?: number;
  style?: StyleProp<ViewStyle>;
}) {
  const reduceMotion = useReducedMotion();
  const opacity = useSharedValue(enabled ? 0 : 1);
  const translateY = useSharedValue(enabled && !reduceMotion ? offset : 0);
  const played = useRef(false);

  useEffect(() => {
    if (!enabled || played.current) return;
    played.current = true;
    if (reduceMotion) {
      opacity.value = withTiming(1, {
        duration: 140,
        easing: Easing.bezier(0.23, 1, 0.32, 1),
      });
      translateY.value = 0;
      return;
    }
    const easing = Easing.bezier(0.23, 1, 0.32, 1);
    opacity.value = withDelay(delay, withTiming(1, { duration: 220, easing }));
    translateY.value = withDelay(delay, withTiming(0, { duration: 220, easing }));
  }, [delay, enabled, reduceMotion, opacity, translateY]);

  const animatedStyle = useAnimatedStyle(() => ({
    opacity: opacity.value,
    transform: [{ translateY: translateY.value }],
  }));

  return <Animated.View style={[containerStyle, animatedStyle]}>{children}</Animated.View>;
}
