// The fuzzy matcher the palettes, @ and the tree's filter share
// (Editor/EditorStore.swift's Fuzzy): the letters as typed, together, beat
// the same letters scattered; in the name beats in the path; word starts
// count most. nil (undefined) when they don't all appear in order.

const BREAKS = " /._-";

export function fuzzyScore(query: string, path: string): number | undefined {
  const needle = [...query.toLowerCase().replace(/\s/g, "")];
  if (!needle.length) return 0;
  const name = path.replace(/\/+$/, "").split("/").pop() ?? path;
  const length = [...path].length;
  // "need" is the word in "Show Agents That Need You", not n…e…e…d.
  const typed = query.toLowerCase().trim();
  for (const [candidate, base] of [[name, 40_000], [path, 30_000]] as const) {
    const at = candidate.toLowerCase().indexOf(typed);
    if (at >= 0) {
      const start = at === 0 || BREAKS.includes(candidate[at - 1]);
      return base + (start ? 5_000 : 0) - length;
    }
  }
  const inName = match(needle, name);
  if (inName !== undefined) return 10_000 + inName * 10 - length;
  const inPath = match(needle, path);
  return inPath === undefined ? undefined : inPath * 10 - length;
}

function match(needle: string[], candidate: string): number | undefined {
  const original = [...candidate];
  const lower = [...candidate.toLowerCase()];
  if (lower.length !== original.length) return candidate.toLowerCase().includes(needle.join("")) ? 1 : undefined;
  let score = 0;
  let found = 0;
  let last = -2;
  for (let i = 0; i < lower.length && found < needle.length; i++) {
    if (lower[i] !== needle[found]) continue;
    let points = 1;
    if (i === last + 1) points += 5;
    const prev = original[i - 1];
    const upper = original[i] !== original[i].toLowerCase() && original[i] === original[i].toUpperCase();
    const prevLower = prev !== undefined && prev === prev.toLowerCase() && prev !== prev.toUpperCase();
    if (i === 0 || BREAKS.includes(prev) || (upper && prevLower)) points += 8;
    score += points;
    last = i;
    found += 1;
  }
  return found === needle.length ? score : undefined;
}
