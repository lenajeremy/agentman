import assert from "node:assert/strict";
import test from "node:test";

import { diffStat, parseDiff } from "./diff.ts";

const sample = [
  "diff --git a/main.go b/main.go",
  "index 65c3e6a..13ebf20 100644",
  "--- a/main.go",
  "+++ b/main.go",
  "@@ -31,6 +31,8 @@ func workspacePath(raw string) {",
  " \tif raw == \"\" {",
  " \t\treturn \".\", nil",
  " \t}",
  "+\tif strings.HasPrefix(raw, \"/\") {",
  "+\t\treturn \"\", errors.New(\"invalid\")",
  "-\told line",
  " \treturn path.Clean(raw), nil",
].join("\n");

test("numbers lines from the hunk header", () => {
  const rows = parseDiff(sample);
  assert.deepEqual(
    rows.map((r) => [r.kind, r.oldNumber, r.newNumber]),
    [
      ["hunk", undefined, undefined],
      ["context", 31, 31],
      ["context", 32, 32],
      ["context", 33, 33],
      ["add", undefined, 34],
      ["add", undefined, 35],
      ["remove", 34, undefined],
      ["context", 35, 36],
    ],
  );
});

test("strips the marker so the gutter can carry it instead", () => {
  const rows = parseDiff(sample);
  assert.equal(rows[4].text, '\tif strings.HasPrefix(raw, "/") {');
  assert.equal(rows[6].text, "\told line");
});

test("drops the preamble above the first hunk", () => {
  const rows = parseDiff(sample);
  assert.ok(!rows.some((r) => r.text.startsWith("diff --git") || r.text.startsWith("+++")));
  assert.equal(rows[0].kind, "hunk");
});

test("counts added and removed lines", () => {
  assert.deepEqual(diffStat(parseDiff(sample)), { added: 2, removed: 1 });
});

test("keeps an empty context line, which git writes without its space", () => {
  const rows = parseDiff("@@ -1,2 +1,2 @@\n a\n\n b");
  assert.deepEqual(rows.map((r) => [r.kind, r.text]), [
    ["hunk", "@@ -1,2 +1,2 @@"],
    ["context", "a"],
    ["context", ""],
    ["context", "b"],
  ]);
});

test("a no-newline marker advances neither counter", () => {
  const rows = parseDiff("@@ -1,1 +1,1 @@\n-a\n\\ No newline at end of file\n+b");
  assert.deepEqual(rows.map((r) => [r.kind, r.oldNumber, r.newNumber]), [
    ["hunk", undefined, undefined],
    ["remove", 1, undefined],
    ["meta", undefined, undefined],
    ["add", undefined, 1],
  ]);
});

test("handles several hunks, resetting the counters each time", () => {
  const rows = parseDiff("@@ -1,1 +1,1 @@\n a\n@@ -80,1 +90,1 @@\n b");
  const context = rows.filter((r) => r.kind === "context");
  assert.deepEqual(context.map((r) => [r.oldNumber, r.newNumber]), [[1, 1], [80, 90]]);
});

test("empty or preamble-only input yields nothing", () => {
  assert.deepEqual(parseDiff(""), []);
  assert.deepEqual(parseDiff("diff --git a/x b/x\n--- a/x\n+++ b/x"), []);
});
