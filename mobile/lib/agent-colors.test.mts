import assert from "node:assert/strict";
import test from "node:test";

import {
  AGENT_HUES,
  agentCardTint,
  agentColor,
  agentColorKey,
  assignHues,
  mixColors,
  parseAgentColorBy,
  projectName,
} from "./agent-colors.ts";

const palette = { needsYou: "#ff6a2b", working: "#3268f5", ok: "#12a66a" };

test("worktrees of a repository are one project", () => {
  assert.equal(projectName("/Users/me/Agentman/Worktrees/shop/fix-cart"), "shop");
  assert.equal(projectName("/Users/me/Agentman/Worktrees/shop/add-pay"), "shop");
  assert.equal(projectName("/Users/me/Code/shop"), "shop");
  assert.equal(agentColorKey("/Users/me/Agentman/Worktrees/shop/fix-cart", "project"), "project|shop");
  assert.equal(agentColorKey("/Users/me/Agentman/Worktrees/shop/fix-cart", "folder"), "folder|/Users/me/Agentman/Worktrees/shop/fix-cart");
  assert.equal(agentColorKey("/w", "state"), null);
});

test("each new project takes the least-used hue", () => {
  const assigned = assignHues({}, AGENT_HUES.map((_, index) => `k${index}`));
  assert.deepEqual(Object.values(assigned), AGENT_HUES.map((_, index) => index));
  // All ten used once: the eleventh starts over, and the twelfth the next.
  const more = assignHues(assigned, ["a", "b"]);
  assert.equal(more.a, 0);
  assert.equal(more.b, 1);
});

test("a colour kept is kept for good", () => {
  const assigned = { a: 3, b: 7 };
  assert.equal(assignHues(assigned, ["a", "b"]), assigned, "nothing new, nothing to save");
  const next = assignHues(assigned, ["c"]);
  assert.equal(next.a, 3);
  assert.equal(next.b, 7);
  assert.equal(next.c, 0);
  // A hue from a time with more of them is given a new one.
  assert.equal(assignHues({ a: 99 }, ["a"]).a, 0);
});

test("the colour follows what the user chose", () => {
  const assigned = { "project|shop": 1, "folder|/w/shop": 2 };
  const agent = { state: "idle" as const, cwd: "/w/shop" };
  assert.equal(agentColor(agent, "none", assigned, palette, "light"), null);
  assert.equal(agentColor(agent, "state", assigned, palette, "light"), palette.ok);
  assert.equal(agentColor({ ...agent, state: "busy" }, "state", assigned, palette, "dark"), palette.working);
  assert.equal(agentColor({ ...agent, state: "waiting_input" }, "state", assigned, palette, "light"), palette.needsYou);
  assert.equal(agentColor(agent, "project", assigned, palette, "dark"), AGENT_HUES[1].dark);
  assert.equal(agentColor(agent, "folder", assigned, palette, "light"), AGENT_HUES[2].light);
  assert.equal(agentColor({ ...agent, cwd: "/w/new" }, "project", assigned, palette, "light"), null, "not given one yet");
});

test("a stored choice that makes no sense is None", () => {
  assert.equal(parseAgentColorBy("project"), "project");
  assert.equal(parseAgentColorBy("blue"), "none");
  assert.equal(parseAgentColorBy(null), "none");
});

test("a wash keeps the surface under it", () => {
  assert.equal(mixColors("#ffffff", "#000000", 0.5), "#808080");
  assert.equal(mixColors("#000000", "#ffffff", 0), "#000000");
  const tint = agentCardTint("#3268f5", "#ffffff");
  assert.match(tint.backgroundColor, /^#[0-9a-f]{6}$/);
  assert.notEqual(tint.backgroundColor, tint.borderColor);
});
