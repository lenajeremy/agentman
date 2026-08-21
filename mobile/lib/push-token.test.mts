import assert from "node:assert/strict";
import test from "node:test";

import { looksLikePushToken } from "./push-token.ts";

const body = "x".repeat(22);

test("accepts the two shapes Expo actually issues", () => {
  assert.ok(looksLikePushToken(`ExponentPushToken[${body}]`));
  assert.ok(looksLikePushToken(`ExpoPushToken[${body}]`));
});

test("rejects anything that is not a bare Expo token", () => {
  for (const value of [
    "",
    "hello",
    `ExponentPushToken[${body}`, // unterminated
    "ExponentPushToken[]", // 19 chars, under the floor
    `https://evil.example.com/ExponentPushToken[${body}]`, // prefix must lead
    ` ExponentPushToken[${body}]`, // no leading whitespace
    `ExponentPushToken[${body}] `, // no trailing whitespace
    `exponentpushtoken[${body}]`, // case matters
  ]) {
    assert.equal(looksLikePushToken(value), false, `accepted ${JSON.stringify(value)}`);
  }
});

test("holds the same length bounds as the daemon", () => {
  // Exactly 20 and exactly 256 are inside; one either side is out. The daemon
  // enforces the same window, and a token it would refuse is not worth a round
  // trip.
  const at = (length: number) =>
    `ExponentPushToken[${"y".repeat(length - 19)}]`;
  assert.equal(at(20).length, 20);
  assert.ok(looksLikePushToken(at(20)));
  assert.ok(looksLikePushToken(at(256)));
  assert.equal(looksLikePushToken(at(19)), false);
  assert.equal(looksLikePushToken(at(257)), false);
});
