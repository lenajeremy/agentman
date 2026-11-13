// node --test web/src/thread/compaction.test.mts (Node strips the types).
import { test } from "node:test";
import assert from "node:assert/strict";
import { isCompactionSummary } from "./compaction.ts";

const summary = "This session is being continued from a previous conversation that ran out of context.\n\nSummary: …";

test("the summary of a compacted conversation is known, as the Mac knows it", () => {
  assert.equal(isCompactionSummary(summary), true);
  // Newer Claude Code opens it with a tag and a line about it.
  assert.equal(isCompactionSummary(`<artifact-content-authored-by-others/>\nThe text below was written by Claude Code.\n${summary}`), true);
});

test("a message that only mentions it isn't the summary", () => {
  assert.equal(isCompactionSummary(`Why does it say "${summary.slice(0, 60)}"?`), false);
  assert.equal(isCompactionSummary(`<b>note</b> ${summary}`), false);
  // Only near the top, as the Mac looks (its first 2000 characters).
  assert.equal(isCompactionSummary(`<tag/>\n${"x".repeat(2100)}\n${summary}`), false);
});
