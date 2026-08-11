import assert from "node:assert/strict";
import { test } from "node:test";

import {
  CATCH_UP_MAX_PAGES,
  needsAnotherPage,
  newestTimestamp,
  type CatchUpState,
} from "./catchup.ts";
import type { Message } from "./protocol.ts";

function message(ts: number): Message {
  return { id: `m${ts}`, sessionId: "claude:s1", role: "assistant", ts };
}

function state(over: Partial<CatchUpState> = {}): CatchUpState {
  return { sessionId: "claude:s1", sinceTs: 1_000, depth: 0, ...over };
}

test("one overlapping page closes the gap", () => {
  // The common case: away for a moment, the newest page reaches back past what
  // was already on screen, so nothing is missing.
  const page = [message(900), message(1_100), message(1_200)];
  assert.equal(needsAnotherPage(page, true, state()), false);
});

test("a page entirely newer than what we had means history is still missing", () => {
  // Away long enough that forty new messages arrived: the oldest of them is
  // still newer than our newest, so the two ranges do not touch.
  const page = [message(5_000), message(5_100)];
  assert.equal(
    needsAnotherPage(page, true, state()),
    true,
    "the feed would have had an invisible hole in it",
  );
});

test("a page touching our newest exactly is enough", () => {
  // Equal timestamps mean the ranges meet; fetching again would loop forever
  // over the same boundary message.
  const page = [message(1_000), message(1_100)];
  assert.equal(needsAnotherPage(page, true, state({ sinceTs: 1_000 })), false);
});

test("stops at the start of the transcript", () => {
  // hasMore false means there is nothing older to ask for. Continuing would
  // re-request the same page forever.
  const page = [message(5_000)];
  assert.equal(needsAnotherPage(page, false, state()), false);
});

test("an empty page stops the walk", () => {
  assert.equal(needsAnotherPage([], true, state()), false);
});

test("the walk is bounded", () => {
  // A day-long absence must not pull an entire transcript down a phone
  // connection; the rest stays reachable by scrolling up.
  const page = [message(9_000)];
  assert.equal(
    needsAnotherPage(page, true, state({ depth: CATCH_UP_MAX_PAGES - 1 })),
    false,
    "an unbounded catch-up would make opening a session arbitrarily expensive",
  );
  // One page before the limit it is still allowed to continue.
  assert.equal(needsAnotherPage(page, true, state({ depth: CATCH_UP_MAX_PAGES - 2 })), true);
});

test("newestTimestamp finds the newest regardless of order", () => {
  assert.equal(newestTimestamp([message(10), message(30), message(20)]), 30);
  // No messages means no catch-up is possible; a zero start would fetch the
  // whole history, which is exactly what the first-visit path is for.
  assert.equal(newestTimestamp([]), 0);
});
