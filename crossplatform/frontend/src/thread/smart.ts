// Typographers' punctuation, as the Mac app's markdown (cmark's "smart"
// option) draws an agent's words: straight quotes curled, -- and --- as
// dashes, ... as an ellipsis. Only in text: code is left as typed.

import type { Block, Inline } from "@shared/markdown";

const PUNCTUATION = /[!-/:-@[-`{-~ -⁯⸀-⹿　-〿]/;
const SPACE = /\s/;

/** cmark's dashes: a run of n hyphens as em and en dashes, ems first. */
function dashes(n: number): string {
  if (n === 1) return "-";
  let em = 0;
  let en = 0;
  if (n % 3 === 0) em = n / 3;
  else if (n % 2 === 0) en = n / 2;
  else if (n % 3 === 2) {
    em = (n - 2) / 3;
    en = 1;
  } else {
    em = (n - 4) / 3;
    en = 2;
  }
  return "—".repeat(em) + "–".repeat(en);
}

/**
 * Curls the quotes and joins the dashes of one paragraph's text, given as
 * pieces (each text node's), with "\u0000" standing for something that isn't
 * text (code): it is neither a space nor punctuation.
 */
function smartPieces(pieces: string[]): string[] {
  const all = pieces.join("");
  const chars = [...all];
  const out: string[] = chars.slice();
  const openers: { quote: string; at: number }[] = [];
  const isSpace = (c: string | undefined) => c === undefined || SPACE.test(c);
  const isPunct = (c: string | undefined) => c !== undefined && c !== "\u0000" && PUNCTUATION.test(c);
  for (let i = 0; i < chars.length; i += 1) {
    const c = chars[i];
    if (c === "-" ) {
      let j = i;
      while (chars[j] === "-") j += 1;
      const run = j - i;
      if (run > 1) {
        const made = dashes(run);
        out[i] = made;
        for (let k = i + 1; k < j; k += 1) out[k] = "";
      }
      i = j - 1;
      continue;
    }
    if (c === "." && chars[i + 1] === "." && chars[i + 2] === ".") {
      out[i] = "…";
      out[i + 1] = "";
      out[i + 2] = "";
      i += 2;
      continue;
    }
    if (c !== "'" && c !== '"') continue;
    const before = chars[i - 1];
    const after = chars[i + 1];
    const left = !isSpace(after) && (!isPunct(after) || isSpace(before) || isPunct(before));
    const right = !isSpace(before) && (!isPunct(before) || isSpace(after) || isPunct(after));
    const canOpen = left && !right && before !== "]" && before !== ")";
    const canClose = right;
    if (c === "'") out[i] = "’";
    else out[i] = canClose ? "”" : "“";
    if (canClose) {
      let found = -1;
      for (let k = openers.length - 1; k >= 0; k -= 1) {
        if (openers[k].quote === c) {
          found = k;
          break;
        }
      }
      if (found >= 0) {
        const opener = openers[found];
        out[opener.at] = c === "'" ? "‘" : "“";
        out[i] = c === "'" ? "’" : "”";
        openers.length = found;
        continue;
      }
    }
    if (canOpen) openers.push({ quote: c, at: i });
  }
  // Back into the pieces they came from.
  const result: string[] = [];
  let at = 0;
  for (const piece of pieces) {
    const n = [...piece].length;
    result.push(out.slice(at, at + n).join(""));
    at += n;
  }
  return result;
}

/** A run of inline content with its text made typographic. */
export function smartInline(nodes: Inline[]): Inline[] {
  const pieces: string[] = [];
  const walk = (list: Inline[]) => {
    for (const node of list) {
      if (node.kind === "text") pieces.push(node.text);
      else if ("children" in node) walk(node.children);
      else if (node.kind === "break") pieces.push(" ");
      else pieces.push("\u0000");
    }
  };
  walk(nodes);
  const made = smartPieces(pieces);
  let next = 0;
  const rebuild = (list: Inline[]): Inline[] =>
    list.map((node) => {
      if (node.kind === "text") return { kind: "text", text: made[next++] };
      if ("children" in node) return { ...node, children: rebuild(node.children) };
      next += 1;
      return node;
    });
  return rebuild(nodes);
}

/** Every block's text made typographic; code blocks as they are. */
export function smartBlocks(blocks: Block[]): Block[] {
  return blocks.map((block): Block => {
    switch (block.kind) {
      case "paragraph":
      case "heading":
        return { ...block, content: smartInline(block.content) };
      case "quote":
        return { ...block, blocks: smartBlocks(block.blocks) };
      case "list":
        return { ...block, items: block.items.map((item) => ({ ...item, blocks: smartBlocks(item.blocks) })) };
      case "table":
        return {
          ...block,
          table: {
            ...block.table,
            header: block.table.header.map(smartInline),
            rows: block.table.rows.map((row) => row.map(smartInline)),
          },
        };
      default:
        return block;
    }
  });
}
