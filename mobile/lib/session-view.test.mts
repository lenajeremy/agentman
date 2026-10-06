import assert from "node:assert/strict";
import test from "node:test";

import type { Message } from "./protocol.ts";
import { modeLabel, modelLabel, showsInFeed, statusChip } from "./session-view.ts";

test("the chip shows the agent's mode and how full its context is", () => {
  assert.deepEqual(statusChip("plan", 42), {
    mode: "plan", context: "42%", label: "plan mode, context 42% full",
  });
  assert.deepEqual(statusChip(undefined, 7), { mode: undefined, context: "7%", label: "context 7% full" });
  assert.deepEqual(statusChip(" accept-edits ", 0), {
    mode: "accept-edits", context: undefined, label: "accept-edits mode",
  });
});

test("a session whose mode can be switched always has the chip that switches it", () => {
  assert.deepEqual(statusChip(undefined, undefined, true), { mode: "mode", context: undefined, label: "mode" });
  assert.equal(statusChip("plan", 0, true)?.mode, "plan");
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

test("modes read as words, not identifiers", () => {
  assert.equal(modeLabel("kiro_default"), "Default");
  assert.equal(modeLabel("kiro_planner"), "Planner");
  assert.equal(modeLabel("kiro_guide"), "Guide");
  assert.equal(modeLabel("accept-edits"), "Accept edits");
  assert.equal(modeLabel("accept edits"), "Accept edits");
  assert.equal(modeLabel("plan"), "Plan");
  assert.equal(modeLabel("default"), "Default");
  // A custom Kiro agent keeps its own name, made readable.
  assert.equal(modeLabel("kiro_release_bot"), "Release bot");
  assert.equal(modeLabel("my-reviewer"), "My reviewer");
});

test("models read as people say them, and names already meant for people are kept", () => {
  assert.equal(modelLabel("claude-opus-5-5"), "Opus 5.5");
  assert.equal(modelLabel("claude-sonnet-4.5"), "Sonnet 4.5");
  assert.equal(modelLabel("claude-haiku-4-5-20251001"), "Haiku 4.5");
  assert.equal(modelLabel("claude-3.5-sonnet"), "Sonnet 3.5");
  assert.equal(modelLabel("gpt-6.1-sol"), "GPT-6.1 Sol");
  assert.equal(modelLabel("gpt-6-luna"), "GPT-6 Luna");
  assert.equal(modelLabel("auto"), "Auto");
  assert.equal(modelLabel("Gemini 3.8 Pro (High)"), "Gemini 3.8 Pro (High)");
  assert.equal(modelLabel("Opus 5.5"), "Opus 5.5");
});
