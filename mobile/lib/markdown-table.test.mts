import assert from "node:assert/strict";
import test from "node:test";

import { alignmentRow, cellWidth, parseTable, splitRow } from "./markdown-table.ts";

test("splits rows with or without outer pipes", () => {
  assert.deepEqual(splitRow("| a | b |"), ["a", "b"]);
  assert.deepEqual(splitRow("a | b"), ["a", "b"]);
  assert.deepEqual(splitRow("|  spaced  |  out  |"), ["spaced", "out"]);
});

test("reads alignment from the rule", () => {
  assert.deepEqual(alignmentRow("|---|:--:|---:|"), ["left", "center", "right"]);
  assert.deepEqual(alignmentRow("| :--- | ---: |"), ["left", "right"]);
});

test("rejects lines that merely contain a pipe", () => {
  assert.equal(alignmentRow("| not a rule |"), null);
  assert.equal(alignmentRow("run a | b to pipe"), null);
  // A lone dash column is a horizontal rule, not a one-column table.
  assert.equal(parseTable(["| x |", "|---|"], 0), null);
  assert.equal(parseTable(["prose with | a pipe", "more prose"], 0), null);
});

test("parses a table and reports where it ends", () => {
  const lines = [
    "| Agent | State |",
    "|-------|-------|",
    "| claude | busy |",
    "| codex | idle |",
    "",
    "after",
  ];
  const found = parseTable(lines, 0);
  assert.ok(found);
  assert.deepEqual(found.table.header, ["Agent", "State"]);
  assert.deepEqual(found.table.rows, [["claude", "busy"], ["codex", "idle"]]);
  assert.equal(found.next, 4);
  assert.equal(lines[found.next], "");
});

test("pads and trims ragged rows to the header width", () => {
  const found = parseTable(
    ["| a | b | c |", "|---|---|---|", "| 1 |", "| 1 | 2 | 3 | 4 |"],
    0,
  );
  assert.ok(found);
  // Dropping short rows would lose data the agent actually produced.
  assert.deepEqual(found.table.rows[0], ["1", "", ""]);
  assert.deepEqual(found.table.rows[1], ["1", "2", "3"]);
});

test("bounds a table an agent might emit without end", () => {
  const lines = ["| a | b |", "|---|---|", ...Array(500).fill("| x | y |")];
  const found = parseTable(lines, 0);
  assert.ok(found);
  assert.equal(found.table.rows.length, 200);
  assert.equal(parseTable(["| " + "c |".repeat(20), "|" + "---|".repeat(20)], 0), null);
});

test("measures cells without counting inline markup", () => {
  assert.equal(cellWidth("**bold**"), 4);
  assert.equal(cellWidth("`code`"), 4);
});
