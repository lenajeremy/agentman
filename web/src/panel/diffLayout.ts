// A file's diff laid out to read (Design/DiffLayout.swift), as T3 Code shows
// one: hunk headers gone, the unchanged stretch before each hunk folded to a
// line saying how long it is, and the words that changed inside a changed
// line marked. Stacked, it is one column; split, what the file was beside
// what it became. Rows come from the phone's parser (mobile/lib/diff.ts),
// which reads diffs as the Mac's Design/Diff.swift does.

import { parseDiff, type DiffKind } from "@shared/diff";

export interface Row {
  id: number;
  kind: DiffKind;
  text: string;
  oldNumber?: number;
  newNumber?: number;
}

export function parseRows(diff: string): Row[] {
  return parseDiff(diff).map((row, id) => ({ id, ...row }));
}

/** Unchanged lines left out before a hunk. */
export interface Fold {
  id: number;
  oldStart: number;
  newStart: number;
  count: number;
}

export function foldLabel(fold: Fold): string {
  return `${fold.count} unmodified line${fold.count === 1 ? "" : "s"}`;
}

/** A line as shown, with the parts of its text (UTF-16 offsets) that differ
 *  from the line it replaced or was replaced by. */
export interface Line {
  key: string;
  row: Row;
  changed: [number, number][];
}

export type Item = { fold: Fold } | { line: Line };
export interface Pair {
  key: string;
  old?: Line;
  new?: Line;
}
export type SplitItem = { fold: Fold } | { pair: Pair };

const HEADER = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/;

/** The rows as items. A fold in `expanded` is shown as its lines, read from
 *  `file` (the file as it is now, a line each), when given. */
export function layoutItems(rows: Row[], expanded: Set<number> = new Set(), file?: string[]): Item[] {
  const items: Item[] = [];
  let nextOld = 1, nextNew = 1, folds = 0;
  for (const row of rows) {
    switch (row.kind) {
      case "meta":
        continue;
      case "hunk": {
        const match = HEADER.exec(row.text);
        if (!match) continue;
        const old = Number(match[1]), now = Number(match[2]);
        // A new or deleted file starts at 0 on its empty side.
        const hidden = old === 0 || now === 0 ? 0 : Math.min(old - nextOld, now - nextNew);
        if (hidden > 0) {
          const fold: Fold = { id: folds++, oldStart: nextOld, newStart: nextNew, count: hidden };
          if (expanded.has(fold.id) && file) {
            for (let offset = 0; offset < hidden; offset++) {
              const number = fold.newStart + offset;
              const text = number - 1 < file.length ? file[number - 1] : "";
              items.push({ line: { key: `x${fold.id}.${offset}`, changed: [], row: { id: -1, kind: "context", text, oldNumber: fold.oldStart + offset, newNumber: number } } });
            }
          } else {
            items.push({ fold });
          }
        }
        nextOld = Math.max(old, 1);
        nextNew = Math.max(now, 1);
        break;
      }
      case "context":
        items.push({ line: { key: `l${row.id}`, row, changed: [] } });
        nextOld = (row.oldNumber ?? nextOld) + 1;
        nextNew = (row.newNumber ?? nextNew) + 1;
        break;
      case "add":
        items.push({ line: { key: `l${row.id}`, row, changed: [] } });
        nextNew = (row.newNumber ?? nextNew) + 1;
        break;
      case "remove":
        items.push({ line: { key: `l${row.id}`, row, changed: [] } });
        nextOld = (row.oldNumber ?? nextOld) + 1;
        break;
    }
  }
  markChangedWords(items);
  return items;
}

/** The same items side by side: a run of removed lines beside the run of
 *  added lines that follows it, line for line. */
export function layoutSplit(items: Item[]): SplitItem[] {
  const result: SplitItem[] = [];
  let removed: Line[] = [];
  let added: Line[] = [];
  const flush = () => {
    for (let i = 0; i < Math.max(removed.length, added.length); i++) {
      const old = removed[i], now = added[i];
      result.push({ pair: { key: "p" + (old ?? now).key, old, new: now } });
    }
    removed = [];
    added = [];
  };
  for (const item of items) {
    if ("fold" in item) {
      flush();
      result.push(item);
      continue;
    }
    const line = item.line;
    if (line.row.kind === "remove") {
      if (added.length) flush();
      removed.push(line);
    } else if (line.row.kind === "add") {
      added.push(line);
    } else {
      flush();
      result.push({ pair: { key: "p" + line.key, old: line, new: line } });
    }
  }
  flush();
  return result;
}

/** The widest line number in the diff, in digits, for its gutter. */
export function digitsOf(rows: Row[]): number {
  const widest = rows.reduce((m, r) => Math.max(m, r.oldNumber ?? 0, r.newNumber ?? 0), 0);
  return Math.max(String(widest).length, 2);
}

function markChangedWords(items: Item[]) {
  let index = 0;
  const kindAt = (i: number) => {
    const item = items[i];
    return item && "line" in item ? item.line.row.kind : undefined;
  };
  while (index < items.length) {
    if (kindAt(index) !== "remove") {
      index++;
      continue;
    }
    const removes: number[] = [];
    while (index < items.length && kindAt(index) === "remove") removes.push(index++);
    const adds: number[] = [];
    while (index < items.length && kindAt(index) === "add") adds.push(index++);
    for (let k = 0; k < Math.min(removes.length, adds.length); k++) {
      const before = (items[removes[k]] as { line: Line }).line;
      const after = (items[adds[k]] as { line: Line }).line;
      const [was, now] = changedWords(before.row.text, after.row.text);
      if (was && now) {
        items[removes[k]] = { line: { ...before, changed: was[0] === was[1] ? [] : [was] } };
        items[adds[k]] = { line: { ...after, changed: now[0] === now[1] ? [] : [now] } };
      }
    }
  }
}

const WORD = /[\p{L}\p{N}_]/u;

/** What differs between a line and the line it became, widened to whole
 *  words; null when they share too little for the mark to say anything. */
export function changedWords(old: string, now: string): [[number, number] | null, [number, number] | null] {
  let prefix = 0;
  while (prefix < old.length && prefix < now.length && old.charCodeAt(prefix) === now.charCodeAt(prefix)) prefix++;
  let suffix = 0;
  while (suffix < old.length - prefix && suffix < now.length - prefix &&
    old.charCodeAt(old.length - 1 - suffix) === now.charCodeAt(now.length - 1 - suffix)) suffix++;
  const was = widen(prefix, old.length - suffix, old);
  const is = widen(prefix, now.length - suffix, now);
  const shared = prefix + suffix;
  if (shared <= 0 || shared / Math.max(old.length, now.length, 1) < 0.3) return [null, null];
  return [was, is];
}

function widen(start: number, end: number, text: string): [number, number] {
  if (start >= end) return [start, end];
  const word = (i: number) => WORD.test(text[i] ?? "");
  while (start > 0 && word(start - 1) && word(start)) start--;
  while (end < text.length && word(end - 1) && word(end)) end++;
  return [start, end];
}
