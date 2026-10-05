import assert from "node:assert/strict";
import { test } from "node:test";

import { failPending } from "./pending-requests.ts";

// A request the Mac can no longer answer used to wait out its 30-second timer
// after the Mac went offline: a download stalled at its percentage, a folder
// spun. Going offline answers them all at once.
test("every request waiting on the Mac fails at once when it goes offline", () => {
  const failed: string[] = [];
  const timers: ReturnType<typeof setTimeout>[] = [];
  const pending = new Map<string, { reject(error: Error): void; timer: ReturnType<typeof setTimeout> }>();
  for (const id of ["chunk", "resume", "folder"]) {
    const timer = setTimeout(() => assert.fail(`${id} waited for its timer`), 50);
    timers.push(timer);
    pending.set(id, { reject: (error) => failed.push(`${id}: ${error.message}`), timer });
  }
  const cancelled: string[] = [];
  failPending(pending, "Your Mac went offline.", (id) => cancelled.push(id));
  assert.deepEqual(failed, [
    "chunk: Your Mac went offline.",
    "resume: Your Mac went offline.",
    "folder: Your Mac went offline.",
  ]);
  assert.deepEqual(cancelled, ["chunk", "resume", "folder"]);
  assert.equal(pending.size, 0);
});
