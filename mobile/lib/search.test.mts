import assert from "node:assert/strict";
import { test } from "node:test";

import { decodeDaemonEvent } from "./protocol.ts";
import { markedSegments, markWords, searchable } from "./search.ts";

test("only words of three letters are looked up", () => {
  assert.equal(searchable("go notarize"), true);
  assert.equal(searchable("to a"), false);
  assert.equal(searchable("  "), false);
  assert.deepEqual(markWords("a notarize no"), ["notarize", "no"]);
});

// The words are found as the daemon found them: case and accents aside,
// each place once.
test("words are found again in what was found", () => {
  const marked = markedSegments("Notarized the Café build; don’t notarize", markWords("notarize cafe don't"));
  assert.deepEqual(marked.filter((s) => s.match).map((s) => s.text), ["Notarize", "Café", "don’t", "notarize"]);
  assert.equal(marked.map((s) => s.text).join(""), "Notarized the Café build; don’t notarize");
  assert.deepEqual(markedSegments("nothing here", ["safari"]), [{ text: "nothing here", match: false }]);
});

test("a search is read, and a malformed one is not", () => {
  const search = {
    query: "safari",
    hits: [
      {
        session: { id: "claude:e", kind: "claude", nativeId: "e", name: "Browser support", cwd: "/tmp/site", state: "ended", inject: "none", startedAt: 1, lastActivityAt: 1 },
        messageId: "x9", role: "user", ts: 1761955200000, snippet: "…Safari 15 is the oldest", matches: 2,
      },
    ],
    indexing: true,
  };
  const event = decodeDaemonEvent({ type: "search", search });
  assert.equal(event?.search?.hits[0].session?.state, "ended");
  assert.equal(decodeDaemonEvent({ type: "search", search: { query: "x", hits: [{ messageId: 4 }] } }), null);
  assert.equal(decodeDaemonEvent({ type: "search" }), null);
});
