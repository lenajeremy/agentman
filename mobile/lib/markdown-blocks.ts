/**
 * Block-level markdown: headings, rules, lists, quotes, fenced code, tables.
 *
 * This lives beside the inline tokeniser rather than in the renderer for the
 * same reason that one does: it is a pure rule that can be tested on Node. It
 * was not always here, and the cost of that showed — headings were capped at
 * three levels, so every `####` an agent wrote rendered as literal hashes in
 * the transcript, and nothing failed because a component had no test.
 *
 * Agent output is untrusted, so every scan is a single left-to-right pass whose
 * cost is bounded by the length of the input.
 */

import { parseTable, type Table } from "./markdown-table.ts";

/** Markdown allows six heading levels; anything deeper is not a heading. */
export const MAX_HEADING_LEVEL = 6;

export type Block =
  | { kind: "paragraph" | "quote" | "code"; text: string }
  | { kind: "heading"; level: number; text: string }
  | { kind: "bullet" | "number"; marker: string; text: string }
  | { kind: "rule" }
  | { kind: "table"; table: Table };

/**
 * Reads an ATX heading, or null when the line is not one.
 *
 * The space after the hashes is required, which is what keeps a comment like
 * `#include` or a bare `#hashtag` from becoming a heading.
 */
export function headingLine(line: string): Block | null {
  let level = 0;
  while (level < MAX_HEADING_LEVEL && line[level] === "#") level += 1;
  if (level === 0 || line[level] !== " ") return null;
  return { kind: "heading", level, text: line.slice(level + 1).trimEnd() };
}

/**
 * Reads an ordered list marker, bounded so a run of digits cannot be scanned
 * indefinitely looking for a `.` that never comes.
 */
export function numberedLine(line: string): Block | null {
  let end = 0;
  while (end < line.length && end < 6 && line.charCodeAt(end) >= 48 && line.charCodeAt(end) <= 57) {
    end += 1;
  }
  if (end === 0 || line[end] !== "." || line[end + 1] !== " ") return null;
  return { kind: "number", marker: line.slice(0, end + 1), text: line.slice(end + 2) };
}

/**
 * Reports whether a line is a thematic break: three or more of `-`, `*` or `_`
 * and nothing else but spaces.
 *
 * A table's delimiter row also runs on dashes, but it always carries a `|`, and
 * in any case the table parser consumes it as part of the table above — so it
 * never reaches this test.
 */
export function isThematicBreak(line: string): boolean {
  const body = line.replace(/ /g, "");
  if (body.length < 3) return false;
  return /^-+$/.test(body) || /^\*+$/.test(body) || /^_+$/.test(body);
}

/** Splits a message into the blocks the renderer draws. */
export function parseBlocks(source: string): Block[] {
  const blocks: Block[] = [];
  const lines = source.replace(/\r\n?/g, "\n").split("\n");
  let paragraph: string[] = [];
  let code: string[] | null = null;

  const flushParagraph = () => {
    if (paragraph.length > 0) {
      blocks.push({ kind: "paragraph", text: paragraph.join("\n") });
      paragraph = [];
    }
  };
  const flushCode = () => {
    if (code !== null) {
      blocks.push({ kind: "code", text: code.join("\n") });
      code = null;
    }
  };

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const trimmed = line.trimStart();
    if (trimmed.startsWith("```")) {
      if (code === null) {
        flushParagraph();
        code = [];
      } else {
        flushCode();
      }
      continue;
    }
    if (code !== null) {
      code.push(line);
      continue;
    }
    if (trimmed === "") {
      flushParagraph();
      continue;
    }

    const heading = headingLine(trimmed);
    if (heading) {
      flushParagraph();
      blocks.push(heading);
      continue;
    }
    // Before the bullet test, which would otherwise read `***` as a list item
    // whose text is `*`.
    if (isThematicBreak(trimmed)) {
      flushParagraph();
      blocks.push({ kind: "rule" });
      continue;
    }
    if (trimmed.startsWith("> ")) {
      flushParagraph();
      blocks.push({ kind: "quote", text: trimmed.slice(2) });
      continue;
    }
    if (/^[-*+]\s/.test(trimmed)) {
      flushParagraph();
      blocks.push({ kind: "bullet", marker: "•", text: trimmed.slice(2) });
      continue;
    }
    const numbered = numberedLine(trimmed);
    if (numbered) {
      flushParagraph();
      blocks.push(numbered);
      continue;
    }
    // Checked late: a table only exists if the next line is a rule, so every
    // cheaper block shape gets to claim the line first.
    const table = parseTable(lines, index);
    if (table) {
      flushParagraph();
      blocks.push({ kind: "table", table: table.table });
      index = table.next - 1;
      continue;
    }
    paragraph.push(line);
  }
  flushParagraph();
  flushCode();
  return blocks;
}
