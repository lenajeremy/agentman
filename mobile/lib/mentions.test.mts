import assert from "node:assert/strict";
import test from "node:test";

import { findFiles, mentionAt, splitPath, withMention } from "./mentions.ts";
import type { DaemonEvent, Request } from "./protocol.ts";

test("a mention is the @word the caret ends", () => {
  assert.deepEqual(mentionAt("look at @src/lo", 15), { start: 8, end: 15, query: "src/lo" });
  assert.deepEqual(mentionAt("@", 1), { start: 0, end: 1, query: "" });
  assert.deepEqual(mentionAt("@log and more", 4), { start: 0, end: 4, query: "log" });
  assert.equal(mentionAt("email me@example.com", 20), null, "an @ inside a word is not a mention");
  assert.equal(mentionAt("look at @src/login.tsx", 12), null, "a caret mid-word is editing it");
  assert.equal(mentionAt("@a@b", 4), null);
  assert.equal(mentionAt("no mention", 10), null);
});

test("a picked file replaces the @word, with a space after it", () => {
  const at = mentionAt("check @logi please", 11)!;
  assert.deepEqual(withMention("check @logi please", at, "app/login.tsx"), {
    text: "check @app/login.tsx please",
    caret: 21,
  });
  const end = mentionAt("see @lo", 7)!;
  assert.deepEqual(withMention("see @lo", end, "app/login.tsx"), { text: "see @app/login.tsx ", caret: 19 });
});

test("files are asked of the Mac for the session", async () => {
  const sent: Request[] = [];
  const ask = async (request: Request): Promise<DaemonEvent> => {
    sent.push(request);
    return { type: "files", files: ["app/login.tsx"] };
  };
  assert.deepEqual(await findFiles(ask, "claude:s", "login"), ["app/login.tsx"]);
  assert.deepEqual(sent, [{ type: "find_files", sessionId: "claude:s", text: "login" }]);
});

test("a path reads as its name and its folder", () => {
  assert.deepEqual(splitPath("app/session/[id].tsx"), { name: "[id].tsx", folder: "app/session" });
  assert.deepEqual(splitPath("README.md"), { name: "README.md", folder: "" });
});
