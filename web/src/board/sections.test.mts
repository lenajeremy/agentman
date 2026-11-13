// node --test web/src/board/sections.test.mts (Node strips the types).
import { test } from "node:test";
import assert from "node:assert/strict";
import { detail, headline, makeBoard, mentioned } from "./sections.ts";
import { checklist, makeDigest, paragraphs } from "./digest.ts";

const agent = (id: string, more: Record<string, unknown> = {}) =>
  ({ id, name: id, kind: "claude", cwd: "/w/" + id, state: "idle", inject: "tmux", lastActivityAt: 0, ...more }) as never;
const stateOf = (s: { state: string; question?: unknown }) => (s.question || s.state === "waiting_input" ? "waiting_input" : s.state === "busy" ? "busy" : "idle") as never;
const inputs = (more = {}) => ({ stateOf, archived: () => false, hidden: new Set<string>(), waiting: new Set<string>(), ...more });

test("agents go to the column for what they want, ended ones nowhere", () => {
  const board = makeBoard([
    agent("asks", { state: "waiting_input" }),
    agent("busy", { state: "busy" }),
    agent("rests"),
    agent("gone", { state: "ended" }),
  ], inputs());
  assert.deepEqual([board.needsYou, board.working, board.idle].map((c) => c.map((s: { id: string }) => s.id)), [["asks"], ["busy"], ["rests"]]);
});

test("a card from Agentman brings an idle agent to Needs you, but not a working one", () => {
  const board = makeBoard([agent("idle"), agent("busy", { state: "busy" })], inputs({ waiting: new Set(["idle", "busy"]) }));
  assert.equal(board.needsYou.length, 1);
  assert.equal(board.working.length, 1);
});

test("hidden and archived agents leave the columns until they need the user", () => {
  const board = makeBoard([agent("hid"), agent("old"), agent("hidAsks", { state: "waiting_input" })],
    inputs({ hidden: new Set(["hid", "hidAsks"]), archived: (s: { id: string }) => s.id === "old" }));
  assert.deepEqual(board.hidden.map((s: { id: string }) => s.id), ["hid"]);
  assert.deepEqual(board.needsYou.map((s: { id: string }) => s.id), ["hidAsks"]);
  assert.equal(board.idle.length, 0);
});

test("the headline and detail say it as the Mac's do", () => {
  const board = makeBoard([agent("a", { state: "waiting_input" }), agent("b", { state: "busy" }), agent("c", { state: "busy" }), agent("d", { state: "busy" }), agent("e")], inputs());
  assert.equal(headline(board, true), "1 agent is waiting on you. The rest are fine.");
  assert.equal(detail(board), "d, c and 1 more are working, 1 is idle.");
  assert.equal(headline(board, false), "Waiting to reach your agents.");
  assert.equal(headline(makeBoard([], inputs()), true), "No agents running.");
});

test("Agentman's latest answer outlines the agents it looked into or named", () => {
  const ids = mentioned([
    { id: "1", role: "user", text: "how is bugs?", ts: 1 },
    { id: "2", role: "activity", sessionId: "x", ts: 2 },
    { id: "3", role: "assistant", text: "Uploader is done.", ts: 3 },
  ] as never, [agent("uploader"), agent("bugs")]);
  assert.deepEqual([...ids].sort(), ["uploader", "x"]);
});

test("a digest reads the turn's last steps, command, checklist and words", () => {
  const m = (id: string, more: Record<string, unknown>) => ({ id, ts: Number(id), role: "tool", ...more });
  const digest = makeDigest([
    m("1", { role: "user", text: "go" }),
    m("2", { tool: { name: "Read", summary: "a.go", status: "ok" } }),
    m("3", { tool: { name: "Bash", summary: "go test ./...", status: "ok", title: "Ran the tests" } }),
    m("4", { tool: { name: "TodoWrite", status: "ok" }, text: "✔ one\n◐ two\n○ three" }),
    m("5", { tool: { name: "Edit", summary: "b.go", status: "running" } }),
    m("6", { role: "assistant", text: "Fixed it.\n\n```go\nx\n```\n\nShall I push?" }),
  ] as never);
  assert.deepEqual(digest.steps.map((s) => `${s.text}:${s.state}`), ["a.go:done", "Ran the tests:done", "b.go:running"]);
  assert.equal(digest.command, "go test ./...");
  assert.deepEqual(digest.progress, { done: 1, total: 3, now: "two" });
  assert.equal(digest.said, "Fixed it.");
  assert.equal(digest.asked, "Shall I push?");
  assert.equal(digest.turnStarted, 1);
});

test("a checklist needs marks on every line, and two items", () => {
  assert.equal(checklist("✔ one"), undefined);
  assert.equal(checklist("✔ one\nplain line"), undefined);
  assert.deepEqual(paragraphs("# Title\n- one\n- two\n\n| a | b |\nlast"), ["one two", "last"]);
});
