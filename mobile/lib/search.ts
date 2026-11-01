/**
 * Search on the phone: what the Mac's index can look up, and the words of a
 * query found again in what it found, to be marked. As the Mac app does it
 * (desktop/Agentman/Design/SearchHighlight.swift).
 */

/** Whether a query has a word the index can look up: three characters or
 *  more. Shorter words only narrow, so on their own they find nothing. */
export function searchable(query: string): boolean {
  return query.split(/\s+/).some((word) => Array.from(word).length >= 3);
}

/** The query's words worth marking, longest first: one letter would mark
 *  half a line, and "notarize" is marked whole rather than as "no". */
export function markWords(query: string): string[] {
  return query
    .split(/\s+/)
    .filter((word) => Array.from(word).length >= 2)
    .sort((a, b) => Array.from(b).length - Array.from(a).length);
}

export interface Segment {
  text: string;
  match: boolean;
}

/** One character as the Mac's index compares it: lower case, accents off,
 *  curly apostrophes straight. Always one character for one, so a place
 *  found in the folded text is the same place in the original. */
function fold(character: string): string {
  if (character === "’" || character === "‘") return "'";
  const lower = character.toLowerCase();
  const bare = lower.normalize("NFD").replace(/\p{M}/gu, "");
  const first = Array.from(bare)[0];
  return first ?? lower;
}

/** Text cut where the words are, each place once, ignoring case and accents
 *  as the daemon matched them. */
export function markedSegments(text: string, words: string[]): Segment[] {
  if (!text || words.length === 0) return [{ text, match: false }];
  const original = Array.from(text);
  const folded = original.map(fold);
  const taken = new Array<boolean>(original.length).fill(false);
  for (const word of words) {
    const needle = Array.from(word).map(fold);
    if (needle.length === 0) continue;
    for (let at = 0; at + needle.length <= folded.length; at++) {
      let found = true;
      for (let j = 0; j < needle.length; j++) {
        if (folded[at + j] !== needle[j] || taken[at + j]) {
          found = false;
          break;
        }
      }
      if (!found) continue;
      for (let j = 0; j < needle.length; j++) taken[at + j] = true;
      at += needle.length - 1;
    }
  }
  const segments: Segment[] = [];
  for (let i = 0; i < original.length; i++) {
    const last = segments[segments.length - 1];
    if (last && last.match === taken[i]) last.text += original[i];
    else segments.push({ text: original[i], match: taken[i] });
  }
  return segments;
}
