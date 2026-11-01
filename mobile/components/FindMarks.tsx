import { createContext, useContext } from "react";
import { Text } from "react-native";

import { useTheme } from "../lib/appearance";
import { markedSegments } from "../lib/search";

/** What a message marks while its conversation's find bar is open: the
 *  words, and whether it is the match being shown. */
export interface FindMarks {
  words: string[];
  current: boolean;
}

export const FindContext = createContext<FindMarks | null>(null);

/**
 * Text with the find bar's words washed in, every match yellow and the one
 * shown orange, as on the Mac. Spans, to sit inside a Text; plain text when
 * nothing is being found.
 */
export function Marked({ text, marks }: { text: string; marks?: FindMarks | null }) {
  const context = useContext(FindContext);
  const { color } = useTheme();
  const using = marks === undefined ? context : marks;
  if (!using || using.words.length === 0) return text;
  const wash = using.current ? color.findCurrent : color.findMatch;
  return markedSegments(text, using.words).map((segment, index) =>
    segment.match ? (
      <Text key={index} style={{ backgroundColor: wash }}>
        {segment.text}
      </Text>
    ) : (
      segment.text
    ),
  );
}
