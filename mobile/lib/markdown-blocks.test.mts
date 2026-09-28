import assert from "node:assert/strict";
import test from "node:test";

import { fenceLanguage, headingLine, isThematicBreak, parseBlocks } from "./markdown-blocks.ts";

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
    {
      kind: "code",
      text: "# or follow a specific session:\nam watch",
      language: "bash",
    },
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

// A fence names its language far more often than not, and it is the only
// thing that can tell a block of Java from a block of prose.
test("fenceLanguage reads the info string", () => {
  assert.equal(fenceLanguage("```java"), "java");
  assert.equal(fenceLanguage("```JAVA"), "java");
  assert.equal(fenceLanguage("```ts"), "ts");
  assert.equal(fenceLanguage("````python"), "python");
  assert.equal(fenceLanguage("``` go "), "go");
  assert.equal(fenceLanguage("```c++"), "c++");
  assert.equal(fenceLanguage("```objective-c"), "objective-c");
});

// Agents write attributes after the language, and a bare fence is common.
test("fenceLanguage takes only the language", () => {
  assert.equal(fenceLanguage("```ts twoslash"), "ts");
  assert.equal(fenceLanguage("```python {highlight=3}"), "python");
  assert.equal(fenceLanguage("```js,jsx"), "js");
  assert.equal(fenceLanguage("```"), "");
  assert.equal(fenceLanguage("```   "), "");
});

test("parseBlocks carries the fence language onto the block", () => {
  const blocks = parseBlocks("```java\nclass A {}\n```");
  assert.equal(blocks.length, 1);
  assert.equal(blocks[0].kind, "code");
  assert.equal(blocks[0].kind === "code" ? blocks[0].language : "", "java");
  assert.equal(blocks[0].kind === "code" ? blocks[0].text : "", "class A {}");
});

// A block with no language still renders; it simply is not highlighted.
test("parseBlocks leaves an unnamed fence without a language", () => {
  const blocks = parseBlocks("```\nplain text\n```");
  assert.equal(blocks[0].kind === "code" ? blocks[0].language : "unset", undefined);
});

// One fence's language must not leak into the next block.
test("parseBlocks does not carry a language past its fence", () => {
  const blocks = parseBlocks("```java\nA\n```\n\ntext\n\n```\nB\n```");
  const code = blocks.filter((block) => block.kind === "code");
  assert.equal(code.length, 2);
  assert.equal(code[0].kind === "code" ? code[0].language : "", "java");
  assert.equal(code[1].kind === "code" ? code[1].language : "unset", undefined);
});
