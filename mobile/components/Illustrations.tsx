import { StyleProp, View, ViewStyle } from "react-native";
import Svg, { Circle, G, Path, Rect } from "react-native-svg";

import { useTheme } from "../lib/appearance";

/**
 * Illustrations are built from one small kit — rounded squares for computers
 * and terminals, a phone outline, quarter circles for rhythm, and a grid of
 * dots where one dot is one agent — in the palette's own colours, so they
 * switch with the theme instead of shipping as bitmaps per scheme.
 */
function useDeviceColours() {
  const { color, scheme } = useTheme();
  const dark = scheme === "dark";
  return {
    color,
    body: dark ? color.fillStrong : color.inverse,
    screen: dark ? "#0B0C0E" : "#1D1F24",
    cursor: dark ? color.workingText : "#8E9DFF",
  };
}

/** A laptop showing a pairing code, linked to a phone that is scanning it. */
export function PairIllustration({ style }: { style?: StyleProp<ViewStyle> }) {
  const { color, body, screen } = useDeviceColours();
  return (
    <View style={style} accessible={false} importantForAccessibility="no-hide-descendants">
      <Svg width="100%" height="100%" viewBox="0 0 340 214">
        <Circle cx={266} cy={120} r={92} fill={color.workingWash} />
        <Path d="M0 214 V132 A82 82 0 0 1 82 214 Z" fill={color.needsYouEdge} />
        <Rect x={150} y={20} width={28} height={28} rx={8} fill={color.workingSoft} />
        <Circle cx={60} cy={34} r={6} fill={color.needsYou} />

        <Rect x={34} y={56} width={160} height={104} rx={12} fill={body} />
        <Rect x={44} y={66} width={140} height={84} rx={6} fill={screen} />
        <G fill="#FFFFFF">
          <Rect x={92} y={78} width={16} height={16} rx={3} />
          <Rect x={120} y={78} width={16} height={16} rx={3} />
          <Rect x={92} y={106} width={16} height={16} rx={3} />
          <Rect x={120} y={106} width={6} height={6} rx={1} />
          <Rect x={130} y={112} width={6} height={6} rx={1} />
          <Rect x={120} y={118} width={6} height={6} rx={1} />
          <Rect x={112} y={98} width={5} height={5} rx={1} />
        </G>
        <G fill={screen}>
          <Rect x={96} y={82} width={8} height={8} rx={2} />
          <Rect x={124} y={82} width={8} height={8} rx={2} />
          <Rect x={96} y={110} width={8} height={8} rx={2} />
        </G>
        <Rect x={22} y={160} width={184} height={12} rx={6} fill={color.fillStrong} />

        <Path
          d="M198 108 H 236"
          stroke={color.working}
          strokeWidth={3.5}
          strokeDasharray="1 8"
          strokeLinecap="round"
        />

        <Rect
          x={238}
          y={56}
          width={72}
          height={134}
          rx={17}
          fill={color.surface}
          stroke={color.text}
          strokeWidth={3}
        />
        <Rect x={262} y={63} width={24} height={6} rx={3} fill={color.text} />
        <G stroke={color.working} strokeWidth={3.5} fill="none" strokeLinecap="round">
          <Path d="M254 102 v-8 a4 4 0 0 1 4 -4 h8" />
          <Path d="M294 102 v-8 a4 4 0 0 0 -4 -4 h-8" />
          <Path d="M254 126 v8 a4 4 0 0 0 4 4 h8" />
          <Path d="M294 126 v8 a4 4 0 0 1 -4 4 h-8" />
        </G>
        <Rect x={256} y={156} width={36} height={8} rx={4} fill={color.fillStrong} />
        <Circle cx={307} cy={58} r={11} fill={color.needsYou} stroke={color.surface} strokeWidth={4} />
      </Svg>
    </View>
  );
}

const GRID_COLUMNS = 9;
const GRID_ROWS = 3;

/** A board of dots with a single agent lit, above a terminal waiting for it. */
export function EmptyIllustration({ style }: { style?: StyleProp<ViewStyle> }) {
  const { color, body, cursor } = useDeviceColours();
  const dots = [];
  for (let row = 0; row < GRID_ROWS; row += 1) {
    for (let column = 0; column < GRID_COLUMNS; column += 1) {
      const lit = row === 1 && column === 4;
      dots.push(
        <Circle
          key={`${row}:${column}`}
          cx={50 + column * 30}
          cy={36 + row * 26}
          r={8}
          fill={lit ? color.working : color.fillStrong}
        />,
      );
    }
  }
  return (
    <View style={style} accessible={false} importantForAccessibility="no-hide-descendants">
      <Svg width="100%" height="100%" viewBox="0 0 340 214">
        <Path d="M340 0 V96 A96 96 0 0 1 244 0 Z" fill={color.workingWash} />
        <Rect x={0} y={160} width={54} height={54} fill={color.needsYouEdge} />
        {dots}
        <Rect x={112} y={118} width={116} height={74} rx={12} fill={body} />
        <Path
          d="M130 144 l10 8 -10 8"
          stroke="#FFFFFF"
          strokeWidth={3.5}
          fill="none"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
        <Rect x={148} y={158} width={18} height={4} rx={2} fill={cursor} />
      </Svg>
    </View>
  );
}
