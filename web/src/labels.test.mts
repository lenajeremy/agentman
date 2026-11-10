// node --test web/src/labels.test.mts (Node strips the types).
import { test } from "node:test";
import assert from "node:assert/strict";
import { agentName, endedSession } from "./labels.ts";

test("agents are named as the Mac names them", () => {
  assert.equal(agentName("muse"), "Muse");
  assert.equal(agentName("cursor-cli"), "Cursor CLI");
  assert.equal(agentName("cursor"), "Cursor");
  assert.equal(agentName("claude"), "Claude Code");
  assert.equal(agentName("gemini"), "Gemini");
  assert.equal(agentName("grok"), "Grok");
});

test("an ended session is listed under its conversation's id, with nothing to answer or send", () => {
  const live = {
    id: "claude:%pane-3", kind: "claude", nativeId: "6f1c2a9e", name: "demo", cwd: "/w/demo",
    state: "idle", inject: "tmux", question: { id: "q1" }, queued: ["later"], lastActivityAt: 5,
  } as never;
  const ended = endedSession(live);
  assert.equal(ended.id, "claude:6f1c2a9e");
  assert.equal(ended.state, "ended");
  assert.equal(ended.inject, "none");
  assert.equal(ended.question, undefined);
  assert.equal(ended.queued, undefined);
  assert.equal(ended.name, "demo");
  // Without the agent's own id, it keeps the one it had.
  assert.equal(endedSession({ ...(live as object), nativeId: "" } as never).id, "claude:%pane-3");
});
