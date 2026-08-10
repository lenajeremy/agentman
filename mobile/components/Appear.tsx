import { useEffect, useRef, useState } from "react";
import { AccessibilityInfo } from "react-native";
import Animated, {
  Easing,
  useAnimatedStyle,
  useSharedValue,
  withTiming,
} from "react-native-reanimated";

/**
 * Fades and lifts a row in the first time it appears.
 *
 * Live output otherwise materializes instantly, which makes it easy to miss
 * that anything changed — the feed just looks different than it did a second
 * ago. A short rise gives the eye something to follow to the new row.
 *
 * Only genuinely new rows animate. Everything already on screen when the
 * session opens is rendered settled, so scrolling through history is not a
 * cascade of fades.
 */
export function Appear({
  children,
  enabled = true,
}: {
  children: React.ReactNode;
  enabled?: boolean;
}) {
  const [reduceMotion, setReduceMotion] = useState(false);
  const opacity = useSharedValue(enabled ? 0 : 1);
  const translateY = useSharedValue(enabled ? 6 : 0);
  const played = useRef(false);

  useEffect(() => {
    void AccessibilityInfo.isReduceMotionEnabled().then(setReduceMotion);
  }, []);

  useEffect(() => {
    if (!enabled || played.current) return;
    played.current = true;
    if (reduceMotion) {
      opacity.value = 1;
      translateY.value = 0;
      return;
    }
    opacity.value = withTiming(1, { duration: 220, easing: Easing.out(Easing.quad) });
    translateY.value = withTiming(0, { duration: 220, easing: Easing.out(Easing.quad) });
  }, [enabled, reduceMotion, opacity, translateY]);

  const style = useAnimatedStyle(() => ({
    opacity: opacity.value,
    transform: [{ translateY: translateY.value }],
  }));

  return <Animated.View style={style}>{children}</Animated.View>;
}
