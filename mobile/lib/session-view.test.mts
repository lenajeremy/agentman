import assert from "node:assert/strict";
import test from "node:test";

import type { Message } from "./protocol.ts";
import { showsInFeed, statusChip } from "./session-view.ts";

test("the chip shows the agent's mode and how full its context is", () => {
  assert.deepEqual(statusChip("plan", 42), {
    mode: "plan", context: "42%", label: "plan mode, context 42% full",
  });
  assert.deepEqual(statusChip(undefined, 7), { mode: undefined, context: "7%", label: "context 7% full" });
  assert.deepEqual(statusChip(" accept-edits ", 0), {
    mode: "accept-edits", context: undefined, label: "accept-edits mode",
  });
});

test("an agent that reported neither gets no chip", () => {
  assert.equal(statusChip(undefined, undefined), null);
  assert.equal(statusChip("  ", 0), null);
});

const message = (over: Partial<Message>): Message => ({
  id: "m", sessionId: "s", role: "assistant", ts: 1, ...over,
});

test("a withdrawn stream preview leaves no row behind", () => {
  assert.ok(!showsInFeed(message({ text: "" })));
  assert.ok(!showsInFeed(message({ text: "  \n " })));
  assert.ok(!showsInFeed(message({})));
  assert.ok(!showsInFeed(message({ text: "", isSidechain: true })));
});

test("everything with something to show keeps its row", () => {
  assert.ok(showsInFeed(message({ text: "Done." })));
  assert.ok(showsInFeed(message({ role: "user", text: "" })));
  assert.ok(showsInFeed(message({ role: "system", text: "" })));
  assert.ok(showsInFeed(message({ role: "tool", tool: { name: "Read" } })));
  assert.ok(showsInFeed(message({ tool: { name: "Read" } })));
});
