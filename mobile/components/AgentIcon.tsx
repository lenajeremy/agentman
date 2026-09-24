import Svg, { Path } from "react-native-svg";

import { agentMarks } from "./agentMarks";
import { useTheme } from "../lib/appearance";

/**
 * The logo of the company behind an agent, so a glance at the list says
 * whether Claude or OpenAI is doing the work. An agent without a mark yet
 * falls back to a generic terminal prompt rather than a blank tile.
 */
export function AgentIcon({ kind, size = 22 }: { kind: string; size?: number }) {
  const { color } = useTheme();
  const mark = agentMarks[kind];

  if (!mark) {
    return (
      <Svg width={size} height={size} viewBox="0 0 24 24" accessibilityLabel={kind}>
        <Path
          d="M5 7l5 5-5 5M12 17h7"
          stroke={color.muted}
          strokeWidth={2}
          strokeLinecap="round"
          strokeLinejoin="round"
          fill="none"
        />
      </Svg>
    );
  }

  const fill = mark.color ?? color.text;
  return (
    <Svg width={size} height={size} viewBox="0 0 24 24" accessibilityLabel={mark.label}>
      {mark.paths.map((path, index) => (
        <Path key={index} d={path.d} fill={fill} fillRule={path.evenOdd ? "evenodd" : "nonzero"} />
      ))}
    </Svg>
  );
}
