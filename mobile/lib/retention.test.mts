import assert from "node:assert/strict";
import test from "node:test";

import type { Message } from "./protocol.ts";
import { mergeRetainedMessages, newlyReachable, sessionsToForget } from "./retention.ts";

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

// A full session list arrives on every reconnect and every return to the
// foreground. Purging every session not in it also emptied the transcript of
// a past session someone was reading, and nothing loaded it again.
test("a snapshot forgets only sessions nobody is looking at", () => {
  const forget = sessionsToForget(
    ["claude:live", "claude:past-open", "claude:past-closed"],
    new Set(["claude:live"]),
    new Set(["claude:past-open"]),
  );
  assert.deepEqual(forget, ["claude:past-closed"]);
});

// Reopening a session keeps its id, so the screen showing it had no reason to
// subscribe again, and its live output never arrived.
test("a watched session that becomes reachable is reported once", () => {
  const ended = { id: "kiro:abc", inject: "none" };
  const running = { id: "kiro:abc", inject: "tmux" };
  const watched = new Set(["kiro:abc"]);
  assert.deepEqual(newlyReachable([], [running], watched), ["kiro:abc"]);
  assert.deepEqual(newlyReachable([ended], [running], watched), ["kiro:abc"]);
  assert.deepEqual(newlyReachable([running], [running], watched), []);
  assert.deepEqual(newlyReachable([], [ended], watched), []);
  assert.deepEqual(newlyReachable([], [running], new Set()), []);
});
