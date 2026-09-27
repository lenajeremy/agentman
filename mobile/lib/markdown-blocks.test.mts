import assert from "node:assert/strict";
import test from "node:test";

import { headingLine, isThematicBreak, parseBlocks } from "./markdown-blocks.ts";

// Regression: headings were capped at three levels, so `####` failed the test
// for a space after the hashes and fell through to a paragraph — the hashes
// rendered as literal text in the transcript. Seen on a real reply that used
// `#### 5. Pair with Your Phone`.
test("reads all six heading levels", () => {
  for (let level = 1; level <= 6; level += 1) {
    const line = `${"#".repeat(level)} Section`;
    assert.deepEqual(headingLine(line), { kind: "heading", level, text: "Section" }, line);
  }
});

test("a seventh hash is not a heading", () => {
  assert.equal(headingLine("####### too deep"), null);
});

test("hashes without a space are not a heading", () => {
  assert.equal(headingLine("#include <stdio.h>"), null);
  assert.equal(headingLine("#hashtag"), null);
  assert.equal(headingLine("####"), null);
});

test("a deep heading in a message becomes a heading block, not a paragraph", () => {
  const blocks = parseBlocks("#### 5. Pair with Your Phone (iOS / Android Expo App)\ntext after");
  assert.deepEqual(blocks[0], {
    kind: "heading",
    level: 4,
    text: "5. Pair with Your Phone (iOS / Android Expo App)",
  });
  assert.deepEqual(blocks[1], { kind: "paragraph", text: "text after" });
});

test("recognises thematic breaks and not lookalikes", () => {
  for (const line of ["---", "***", "___", "- - -", "-----"]) {
    assert.equal(isThematicBreak(line), true, line);
  }
  for (const line of ["--", "-", "- item", "|---|---|", "—-", ""]) {
    assert.equal(isThematicBreak(line), false, line);
  }
});

test("a rule between paragraphs is its own block", () => {
  assert.deepEqual(parseBlocks("before\n\n---\n\nafter"), [
    { kind: "paragraph", text: "before" },
    { kind: "rule" },
    { kind: "paragraph", text: "after" },
  ]);
});

// The bullet test would read `***` as a list item whose text is `*`, so the
// order of the two checks matters.
test("a thematic break is not mistaken for a bullet", () => {
  assert.deepEqual(parseBlocks("***"), [{ kind: "rule" }]);
  assert.deepEqual(parseBlocks("* item"), [{ kind: "bullet", marker: "•", text: "item" }]);
});

test("hashes inside a fenced block stay code", () => {
  const blocks = parseBlocks("```bash\n# or follow a specific session:\nam watch\n```");
  assert.deepEqual(blocks, [
    { kind: "code", text: "# or follow a specific session:\nam watch" },
  ]);
});

// A table's delimiter row is dashes too, so the rule test must not eat it.
test("a table still parses with the rule test in front of it", () => {
  const blocks = parseBlocks("| a | b |\n| --- | --- |\n| 1 | 2 |");
  assert.equal(blocks.length, 1);
  assert.equal(blocks[0].kind, "table");
});

test("ordered and unordered lists are unchanged", () => {
  assert.deepEqual(parseBlocks("1. first\n- second"), [
    { kind: "number", marker: "1.", text: "first" },
    { kind: "bullet", marker: "•", text: "second" },
  ]);
});
