// node --test web/src/thread/queue.test.mts (Node strips the types).
import { test } from "node:test";
import assert from "node:assert/strict";
import { queuedLabel, stillQueued } from "./queue.ts";

test("a queued message is shown by its words, after how many images went with it", () => {
  assert.equal(queuedLabel({ words: "look", images: 2 }), "2 images · look");
  assert.equal(queuedLabel({ words: "look", images: 1 }), "1 image · look");
  assert.equal(queuedLabel({ words: "look", images: 0 }), "look");
  assert.equal(queuedLabel({ words: "", images: 3 }), "3 images");
});

// As the Mac's testQueuedMessagesKeepTheirWords: words that look like a
// label, words with a picture, and pictures alone each leave the queue when
// the agent's copy comes, the pictures' paths ahead of the words.
test("each leaves the queue when the agent's copy comes, found by its words", () => {
  const waiting = [
    { words: "2 images", images: 0 },
    { words: "1 image · fix this", images: 1 },
    { words: "", images: 2 },
  ];
  const said = (id: string, text: string) => ({ id, sessionId: "claude:s1", role: "user", ts: 1, text }) as never;
  assert.deepEqual(stillQueued(waiting, [
    said("a", "2 images"),
    said("b", "/Users/me/.agentman/images/claude-s1/x.png 1 image · fix this"),
    said("c", "/Users/me/.agentman/images/claude-s1/y.png"),
  ]), []);
  // The agent's replies don't count, and one copy takes one.
  const reply = { id: "r", sessionId: "claude:s1", role: "assistant", ts: 1, text: "2 images" } as never;
  assert.deepEqual(stillQueued([{ words: "again", images: 0 }, { words: "again", images: 0 }], [reply, said("d", "again")]), [{ words: "again", images: 0 }]);
});
