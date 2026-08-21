/**
 * GitHub-flavoured markdown tables.
 *
 * Parsing lives here rather than in the renderer so it can be tested on Node
 * like the other pure rules in this directory. Agent output is untrusted, so
 * everything is bounded: a malformed or enormous table degrades to ordinary
 * paragraph text rather than producing an unbounded grid.
 */

export type Alignment = "left" | "center" | "right";

export interface Table {
  header: string[];
  align: Alignment[];
  rows: string[][];
}

/** A table wider than this is not readable on a phone at any zoom. */
export const MAX_COLUMNS = 12;
/** Beyond this the feed stops being scrollable in any useful way. */
export const MAX_ROWS = 200;

/** Splits one `| a | b |` line, tolerating the outer pipes being absent. */
export function splitRow(line: string): string[] {
  let text = line.trim();
  if (text.startsWith("|")) text = text.slice(1);
  if (text.endsWith("|") && !text.endsWith("\\|")) text = text.slice(0, -1);
  return text.split("|").map((cell) => cell.trim());
}

/**
 * Reads the `|---|:--:|---:|` rule under a header, returning per-column
 * alignment. Null when the line is not a rule, which is what distinguishes a
 * real table from a paragraph that happens to contain a pipe.
 */
export function alignmentRow(line: string): Alignment[] | null {
  if (!line.includes("-")) return null;
  const cells = splitRow(line);
  if (cells.length === 0) return null;
  const align: Alignment[] = [];
  for (const cell of cells) {
    if (!/^:?-+:?$/.test(cell)) return null;
    const left = cell.startsWith(":");
    const right = cell.endsWith(":") && cell.length > 1;
    align.push(left && right ? "center" : right ? "right" : "left");
  }
  return align;
}

/**
 * Reads a table starting at `start`, or null if one does not begin there.
 *
 * Returns the index just past the table so the caller can continue. Rows are
 * padded or trimmed to the header's width: real agent output produces ragged
 * rows often enough that dropping them would lose data the user asked for.
 */
export function parseTable(
  lines: string[],
  start: number,
): { table: Table; next: number } | null {
  const headerLine = lines[start];
  if (headerLine === undefined || !headerLine.includes("|")) return null;

  const align = alignmentRow(lines[start + 1] ?? "");
  if (!align) return null;

  const header = splitRow(headerLine);
  // A single column is far more likely to be prose containing a pipe than a
  // table, and the rule below it would have to be a lone "---" — which is a
  // horizontal rule, not a table.
  if (header.length < 2 || header.length !== align.length) return null;
  if (header.length > MAX_COLUMNS) return null;

  const rows: string[][] = [];
  let cursor = start + 2;
  while (cursor < lines.length && rows.length < MAX_ROWS) {
    const line = lines[cursor];
    if (line.trim() === "" || !line.includes("|")) break;
    const cells = splitRow(line);
    rows.push(
      Array.from({ length: header.length }, (_, index) => cells[index] ?? ""),
    );
    cursor += 1;
  }

  return { table: { header, align, rows }, next: cursor };
}

/**
 * Approximate width of a cell in characters, ignoring inline markup that will
 * not be drawn. Used to size columns: React Native has no table layout, so the
 * columns are measured here and given fixed widths, which is what keeps cells
 * in one column lined up with each other.
 */
export function cellWidth(text: string): number {
  return text.replace(/\*\*/g, "").replace(/`/g, "").length;
}
