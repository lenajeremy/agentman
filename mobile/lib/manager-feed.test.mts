import assert from "node:assert/strict";
import test from "node:test";

import { applyDelta, cardQuestion, chatItems, isNews, prependOlder, unreadCount, upsertEntry } from "./manager-feed.ts";
import type { ManagerEntry } from "./protocol.ts";

const entry = (id: string, role: string, text = "", extra: Partial<ManagerEntry> = {}): ManagerEntry =>
  ({ id, role, text, ts: Number(id.replace(/\D/g, "")) || 1, ...extra });

test("a reply streams into its placeholder and is replaced whole when done", () => {
  let entries = upsertEntry([], entry("a1", "assistant", "", { streaming: true }));
  entries = applyDelta(entries, "a1", "Two agents ");
  entries = applyDelta(entries, "a1", "are working.");
  assert.equal(entries[0].text, "Two agents are working.");
  // A late streaming copy does not wipe what is shown; the finished one wins.
  entries = upsertEntry(entries, entry("a1", "assistant", "", { streaming: true }));
  assert.equal(entries[0].text, "Two agents are working.");
  entries = upsertEntry(entries, entry("a1", "assistant", "Two agents are working, one needs you."));
  assert.equal(entries.length, 1);
  assert.equal(entries[0].text, "Two agents are working, one needs you.");
});

test("older pages join in front without repeats", () => {
  const entries = prependOlder([entry("u3", "user", "c")], [entry("u1", "user", "a"), entry("u3", "user", "c")]);
  assert.deepEqual(entries.map((item) => item.id), ["u1", "u3"]);
});

test("news is a finished reply, an agent's word, or a card still open", () => {
  assert.equal(isNews(entry("a1", "assistant", "done")), true);
  assert.equal(isNews(entry("a2", "assistant", "half", { streaming: true })), false);
  assert.equal(isNews(entry("b3", "brief", "finished the build")), true);
  assert.equal(isNews(entry("x4", "activity", "Read 2 agents")), false);
  const card = (state: string) => entry("c5", "card", "", {
    card: { id: "k", kind: "question", state, sessionId: "s", agentName: "a", title: "t" },
  });
  assert.equal(isNews(card("open")), true);
  assert.equal(isNews(card("answered")), false);
  assert.equal(unreadCount([entry("a1", "assistant", "x"), entry("a9", "assistant", "y")], 5), 1);
});

test("activity folds into runs, images and gone cards stand apart", () => {
  const items = chatItems([
    entry("u1", "user", "what's everyone doing?"),
    entry("x2", "activity", "Read 3 agents", { tool: "list_agents" }),
    entry("x3", "activity", "Read agentman-58", { tool: "read_messages" }),
    entry("x4", "activity", "Looked at a screenshot", { sessionId: "s", path: "/tmp/a.png" }),
    entry("c5", "card", "", { card: { id: "k", kind: "question", state: "settled", sessionId: "s", agentName: "a", title: "t" } }),
    entry("a6", "assistant", "All quiet."),
  ]);
  assert.deepEqual(items.map((item) => item.kind === "activities" ? `run ${item.entries.length}` : `${item.kind} ${item.key}`),
    ["entry u1", "run 2", "image x4", "entry a6"]);
});

test("a question card becomes the agent's question; a key card asks to give the key", () => {
  const base = { sessionId: "s", agentName: "a", title: "wants to run `git push`", state: "open" };
  const options = [{ key: "1", label: "Yes" }, { key: "2", label: "No" }];
  assert.deepEqual(cardQuestion({ ...base, id: "c1", kind: "question", questionId: "q7", options, multiple: false }),
    { id: "q7", prompt: "", options, multiple: false, custom: undefined });
  assert.equal(cardQuestion({ ...base, id: "c2", kind: "question", options }), null, "no question id, nothing to answer");
  assert.equal(cardQuestion({ ...base, id: "c3", kind: "key", options })?.id, "c3");
  assert.equal(cardQuestion({ ...base, id: "c4", kind: "needs_you" }), null);
});
