import assert from "node:assert/strict";
import test from "node:test";

import type { Message } from "./protocol.ts";
import { mergeRetainedMessages } from "./retention.ts";

function message(id: string, ts: number, text = id): Message {
  return { id, sessionId: "s", role: "assistant", ts, text };
}

test("retention deduplicates updates and keeps the newest bounded window", () => {
  const result = mergeRetainedMessages(
    [message("one", 1), message("two", 2), message("three", 3)],
    [message("two", 2, "updated"), message("four", 4)],
    3,
  );
  assert.equal(result.limited, true);
  assert.deepEqual(result.messages.map((item) => item.id), ["two", "three", "four"]);
  assert.equal(result.messages[0].text, "updated");
});

test("retention reports an uncapped page honestly", () => {
  const result = mergeRetainedMessages([], [message("one", 1)], 3);
  assert.equal(result.limited, false);
  assert.deepEqual(result.messages, [message("one", 1)]);
});
