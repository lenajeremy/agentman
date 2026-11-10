// Finds a file by a few of its letters in order (EditorStore.swift's Fuzzy):
// "atv" finds AgentThreadView.swift. Letters in the file's name, at the
// start of words and next to each other count most, and shorter paths win
// ties.

const WORD_EDGE = " /._-";

export function fuzzyScore(query: string, path: string): number | null {
  const needle = [...query.toLowerCase()].filter((c) => !/\s/.test(c));
  if (needle.length === 0) return 0;
  const name = path.slice(path.lastIndexOf("/") + 1);
  // The letters as typed, together, beat the same letters scattered.
  const typed = query.toLowerCase().trim();
  for (const [candidate, base] of [[name, 40_000], [path, 30_000]] as const) {
    const at = candidate.toLowerCase().indexOf(typed);
    if (at >= 0) {
      const start = at === 0 || WORD_EDGE.includes(candidate[at - 1]);
      return base + (start ? 5_000 : 0) - path.length;
    }
  }
  const inName = match(needle, name);
  if (inName !== null) return 10_000 + inName * 10 - path.length;
  const inPath = match(needle, path);
  if (inPath === null) return null;
  return inPath * 10 - path.length;
}

function match(needle: string[], candidate: string): number | null {
  const original = [...candidate];
  const lower = [...candidate.toLowerCase()];
  if (lower.length !== original.length) {
    return candidate.toLowerCase().includes(needle.join("")) ? 1 : null;
  }
  let score = 0;
  let found = 0;
  let last = -2;
  for (let index = 0; index < lower.length && found < needle.length; index++) {
    if (lower[index] !== needle[found]) continue;
    let points = 1;
    if (index === last + 1) points += 5;
    const prev = original[index - 1];
    if (index === 0 || "/._- ".includes(prev) || (isUpper(original[index]) && isLower(prev))) points += 8;
    score += points;
    last = index;
    found += 1;
  }
  return found === needle.length ? score : null;
}

const isUpper = (c: string) => c !== c.toLowerCase() && c === c.toUpperCase();
const isLower = (c: string | undefined) => !!c && c !== c.toUpperCase() && c === c.toLowerCase();

/** The best matches for a query, best first. */
export function fuzzyFilter(query: string, list: string[], limit: number): string[] {
  return list
    .map((path) => [path, fuzzyScore(query, path)] as const)
    .filter((pair): pair is readonly [string, number] => pair[1] !== null)
    .sort((a, b) => b[1] - a[1])
    .slice(0, limit)
    .map((pair) => pair[0]);
}
