import assert from "node:assert/strict";
import test from "node:test";

import {
  base64FromDataUri,
  canAttachMore,
  decodedLength,
  fitWithin,
  MAX_ATTACHMENTS,
  MAX_DIMENSION,
} from "./attachments.ts";

test("an image already within the limit is not resized", () => {
  assert.equal(fitWithin({ width: 800, height: 600 }), null);
  assert.equal(fitWithin({ width: MAX_DIMENSION, height: 900 }), null);
});

test("the longest edge decides, and the ratio is kept", () => {
  assert.deepEqual(fitWithin({ width: 3200, height: 2400 }), { width: 1600, height: 1200 });
  assert.deepEqual(fitWithin({ width: 2400, height: 3200 }), { width: 1200, height: 1600 });
});

// An iPhone screenshot is far taller than it is wide; scaling by width would
// leave it enormous.
test("a tall screenshot is bounded by its height", () => {
  const got = fitWithin({ width: 1290, height: 2796 });
  assert.deepEqual(got, { width: 738, height: 1600 });
});

test("an unknown size is left alone rather than guessed at", () => {
  assert.equal(fitWithin({ width: 0, height: 0 }), null);
  assert.equal(fitWithin({ width: 100, height: 0 }), null);
});

test("an extreme ratio never rounds an edge to zero", () => {
  const got = fitWithin({ width: 10000, height: 3 });
  assert.ok(got && got.height >= 1, `height was ${got?.height}`);
});

test("a data uri yields its payload", () => {
  assert.equal(base64FromDataUri("data:image/png;base64,AAAB"), "AAAB");
  assert.equal(base64FromDataUri("data:image/jpeg;base64,AA=="), "AA==");
});

// A malformed or hostile clipboard must not become a request body.
test("anything that is not an image data uri yields nothing", () => {
  for (const uri of [
    "file:///etc/passwd",
    "data:text/html;base64,AAAA",
    "data:image/svg+xml;base64,AAAA",
    "AAAA",
    "",
  ]) {
    assert.equal(base64FromDataUri(uri), "", uri);
  }
});

test("decoded length accounts for padding", () => {
  assert.equal(decodedLength(""), 0);
  assert.equal(decodedLength("AAAA"), 3);
  assert.equal(decodedLength("AAA="), 2);
  assert.equal(decodedLength("AA=="), 1);
});

test("the attachment count stops at the daemon's limit", () => {
  assert.equal(canAttachMore(0), true);
  assert.equal(canAttachMore(MAX_ATTACHMENTS - 1), true);
  assert.equal(canAttachMore(MAX_ATTACHMENTS), false);
});
