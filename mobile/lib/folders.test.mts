import assert from "node:assert/strict";
import { test } from "node:test";

import { agentCount, folderLabel, mergeCounts, withinFolder } from "./folders.ts";
import { ago } from "./theme.ts";

// Selecting a folder means the project, so its subtree comes with it. The Mac
// applies the same rule in internal/source/history.go; this is what keeps a
// session that arrives while the folder is open from being judged differently
// than one that was fetched with it.
test("withinFolder matches the subtree and nothing else", () => {
  assert.equal(withinFolder("/code/api", "/code/api"), true);
  assert.equal(withinFolder("/code/api/bin", "/code/api"), true);
  assert.equal(withinFolder("/code/api/a/b/c", "/code/api"), true);
  assert.equal(withinFolder("/code/api/", "/code/api"), true);
  assert.equal(withinFolder("/code/api", "/code/api/"), true);
});

// The separator is the whole point: a sibling that merely starts with the
// same letters is a different project.
test("withinFolder does not match a sibling sharing a prefix", () => {
  assert.equal(withinFolder("/code/api-old", "/code/api"), false);
  assert.equal(withinFolder("/code/apiary", "/code/api"), false);
  assert.equal(withinFolder("/code", "/code/api"), false);
  assert.equal(withinFolder("/other/api", "/code/api"), false);
});

test("withinFolder refuses an empty side rather than matching everything", () => {
  assert.equal(withinFolder("", "/code/api"), false);
  assert.equal(withinFolder("/code/api", ""), false);
});

test("folderLabel keeps the last two segments", () => {
  assert.equal(folderLabel("/Users/mac/Desktop/agentman"), "~/Desktop/agentman");
  assert.equal(folderLabel("/Users/mac/Desktop/agentman/"), "~/Desktop/agentman");
  assert.equal(folderLabel("/Users/mac"), "/Users/mac");
  assert.equal(folderLabel("/tmp"), "/tmp");
  assert.equal(folderLabel("/"), "/");
});

test("agentCount says agent once and agents otherwise", () => {
  assert.equal(agentCount(1), "1 agent");
  assert.equal(agentCount(0), "0 agents");
  assert.equal(agentCount(22), "22 agents");
});

// Counts arrive separately from names because only folders that have agents
// get one: sending a zero for every untouched folder in a home directory
// would be most of the payload.
test("mergeCounts pairs a folder with its count and leaves the rest bare", () => {
  const merged = mergeCounts(
    ["Desktop", "Documents", "Movies"],
    [
      { path: "Desktop", agents: 54, running: 3 },
      { path: "Documents", agents: 122 },
    ],
  );
  assert.deepEqual(merged, [
    { name: "Desktop", agents: 54, running: 3 },
    { name: "Documents", agents: 122, running: undefined },
    { name: "Movies" },
  ]);
});

test("mergeCounts keeps the browse order, not the count order", () => {
  const merged = mergeCounts(
    ["a", "b"],
    [{ path: "b", agents: 9 }, { path: "a", agents: 1 }],
  );
  assert.deepEqual(merged.map((folder) => folder.name), ["a", "b"]);
});

// A Mac that predates counts sends none, and browsing still has to work.
test("mergeCounts survives a listing with no counts at all", () => {
  assert.deepEqual(mergeCounts(["Desktop"], []), [{ name: "Desktop" }]);
});

// "412d" is a number nobody converts. A folder's history is mostly old, so
// this is the common case there rather than an edge.
test("ago turns into a date past a month", () => {
  const now = Date.UTC(2026, 8, 28, 12, 0, 0);
  const day = 24 * 60 * 60 * 1000;
  assert.equal(ago(now - 5_000, now), "now");
  assert.equal(ago(now - 90_000, now), "1m");
  assert.equal(ago(now - 3 * 60 * 60 * 1000, now), "3h");
  assert.equal(ago(now - 4 * day, now), "4d");
  assert.equal(ago(now - 30 * day, now), "30d");
  // Past thirty days, a date. 55 days before 28 September 2026 is 4 August.
  assert.equal(ago(now - 55 * day, now), "4 Aug");
});

test("ago names the year only when it is not this one", () => {
  const now = Date.UTC(2026, 8, 28, 12, 0, 0);
  const day = 24 * 60 * 60 * 1000;
  assert.equal(ago(now - 400 * day, now), "24 Aug 25");
});

test("ago says nothing for a session with no recorded time", () => {
  assert.equal(ago(0), "");
});
