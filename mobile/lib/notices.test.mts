import assert from "node:assert/strict";
import test from "node:test";

import { noticeOf } from "./notices.ts";

test("a background task's end reads as what ran and how it ended", () => {
  assert.deepEqual(noticeOf('Background command "Build the app" completed (exit code 0)'), {
    label: "Build the app",
    outcome: "finished",
    tone: "done",
  });
  assert.deepEqual(noticeOf('Background command "Run the tests" completed (exit code 2)'), {
    label: "Run the tests",
    outcome: "failed (exit 2)",
    tone: "failed",
  });
  assert.equal(noticeOf('Background command "Serve the site" was killed').outcome, "stopped");
  // Quotes inside the description stay part of it.
  assert.equal(noticeOf('Background command "Wait for PR "108" checks" completed (exit code 0)').label, 'Wait for PR "108" checks');
});

test("any other system line is said as it is", () => {
  assert.deepEqual(noticeOf("Conversation compacted"), { label: "Conversation compacted", tone: "info" });
});
