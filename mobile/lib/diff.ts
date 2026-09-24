/**
 * Unified diff → rows carrying their own line numbers.
 *
 * Parsing lives here rather than in the screen so it can be tested on Node
 * like the other pure rules in this directory. Without the numbers a phone
 * diff says what changed but never where, which is the first thing you want
 * to know when an agent edited a file you know well.
 */

export type DiffKind = "meta" | "hunk" | "add" | "remove" | "context";

export interface DiffRow {
  kind: DiffKind;
  /** The line with its leading +/-/space removed, ready to render. */
  text: string;
  /** Line number in the original file, absent for an added line. */
  oldNumber?: number;
  /** Line number in the working tree, absent for a removed line. */
  newNumber?: number;
}

const HUNK = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/;

/**
 * Rows before the first `@@` are the `diff --git`/`---`/`+++` preamble. They
 * carry no line numbers and say nothing a reader of one file needs, so they
 * are dropped rather than rendered as noise above every diff.
 */
export function parseDiff(diff: string): DiffRow[] {
  const rows: DiffRow[] = [];
  let oldNumber = 0;
  let newNumber = 0;
  let started = false;

  for (const line of diff.split("\n")) {
    const hunk = HUNK.exec(line);
    if (hunk) {
      started = true;
      oldNumber = Number(hunk[1]);
      newNumber = Number(hunk[2]);
      rows.push({ kind: "hunk", text: line });
      continue;
    }
    if (!started) continue;

    // "\ No newline at end of file" belongs to the line above and advances
    // neither counter.
    if (line.startsWith("\\")) {
      rows.push({ kind: "meta", text: line });
      continue;
    }
    if (line.startsWith("+")) {
      rows.push({ kind: "add", text: line.slice(1), newNumber: newNumber++ });
      continue;
    }
    if (line.startsWith("-")) {
      rows.push({ kind: "remove", text: line.slice(1), oldNumber: oldNumber++ });
      continue;
    }
    // git omits the leading space on a fully empty context line, so an empty
    // string is context rather than something to skip.
    rows.push({
      kind: "context",
      text: line.startsWith(" ") ? line.slice(1) : line,
      oldNumber: oldNumber++,
      newNumber: newNumber++,
    });
  }

  // A trailing newline in the diff text yields one empty context row that was
  // never part of the file.
  const last = rows[rows.length - 1];
  if (last && last.kind === "context" && last.text === "") rows.pop();
  return rows;
}

/** Added and removed counts, for a summary line above the diff. */
export function diffStat(rows: DiffRow[]): { added: number; removed: number } {
  let added = 0;
  let removed = 0;
  for (const row of rows) {
    if (row.kind === "add") added += 1;
    else if (row.kind === "remove") removed += 1;
  }
  return { added, removed };
}
