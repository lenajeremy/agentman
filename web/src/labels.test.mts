// node --test web/src/labels.test.mts (Node strips the types).
import { test } from "node:test";
import assert from "node:assert/strict";
import { agentName } from "./labels.ts";

test("agents are named as the Mac names them", () => {
  assert.equal(agentName("muse"), "Muse");
  assert.equal(agentName("cursor-cli"), "Cursor CLI");
  assert.equal(agentName("cursor"), "Cursor");
  assert.equal(agentName("claude"), "Claude Code");
  assert.equal(agentName("gemini"), "Gemini");
  assert.equal(agentName("grok"), "Grok");
});

