import { ReactNode } from "react";
import {
  GestureResponderEvent,
  Pressable,
  PressableProps,
  StyleProp,
  ViewStyle,
} from "react-native";
import Animated, {
  Easing,
  useAnimatedStyle,
  useReducedMotion,
  useSharedValue,
  withTiming,
} from "react-native-reanimated";

const AnimatedPressable = Animated.createAnimatedComponent(Pressable);

type Props = Omit<PressableProps, "children" | "style"> & {
  children: ReactNode;
  style?: StyleProp<ViewStyle>;
  /** Cards move less than compact controls so the feedback never feels toy-like. */
  pressedScale?: number;
};

/**
 * Immediate, interruptible touch feedback shared by every important control.
 *
 * Only transform is animated, and reduced-motion users keep the native pressed
 * state without spatial movement. Retargeting the same shared value means a
 * quick press/release continues from the value currently on screen.
 */
export function MotionPressable({
  children,
  disabled,
  onPressIn,
  onPressOut,
  pressedScale = 0.97,
  style,
  ...props
}: Props) {
  const scale = useSharedValue(1);
  const reduceMotion = useReducedMotion();

  const animatedStyle = useAnimatedStyle(() => ({
    transform: [{ scale: scale.value }],
  }));

  const pressIn = (event: GestureResponderEvent) => {
    if (!disabled && !reduceMotion) {
      scale.value = withTiming(pressedScale, {
        duration: 110,
        easing: Easing.bezier(0.23, 1, 0.32, 1),
      });
    }
    onPressIn?.(event);
  };

  const pressOut = (event: GestureResponderEvent) => {
    if (!reduceMotion) {
      scale.value = withTiming(1, {
        duration: 140,
        easing: Easing.bezier(0.23, 1, 0.32, 1),
      });
    }
    onPressOut?.(event);
  };

  return (
    <AnimatedPressable
      {...props}
      disabled={disabled}
      onPressIn={pressIn}
      onPressOut={pressOut}
      style={[style, animatedStyle]}
    >
      {children}
    </AnimatedPressable>
  );
}
