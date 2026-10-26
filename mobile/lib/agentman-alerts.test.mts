import assert from "node:assert/strict";
import { test } from "node:test";

import { agentmanAlert, replyAlert } from "./agentman-alerts.ts";
import type { ManagerEntry } from "./protocol.ts";

const entry = (fields: Partial<ManagerEntry>): ManagerEntry => ({ id: "e", role: "assistant", text: "", ts: 1, ...fields });
const card = (kind: string, state = "open") => ({
  id: "c", kind, state, sessionId: "claude:a", agentName: "site", title: "wants to run `git push`",
});

test("a card that needs you rings as needing you, and a settled one doesn't ring", () => {
  assert.deepEqual(agentmanAlert(entry({ role: "card", card: card("question") as never })), {
    kind: "needsYou",
    title: "site needs you",
    body: "wants to run `git push`",
  });
  assert.equal(agentmanAlert(entry({ role: "card", card: card("question", "answered") as never })), null);
  assert.equal(agentmanAlert(entry({ role: "card", card: card("note") as never }))?.kind, "agentman");
});

test("briefs and word from agents are Agentman's to tell; the rest of the feed isn't news", () => {
  assert.deepEqual(agentmanAlert(entry({ role: "brief", text: "site finished the header." })), {
    kind: "agentman",
    title: "Agentman",
    body: "site finished the header.",
  });
  assert.equal(agentmanAlert(entry({ role: "agent", text: "Deployed." }))?.body, "Deployed.");
  for (const role of ["user", "activity", "assistant", "error"]) {
    assert.equal(agentmanAlert(entry({ role, text: "x" })), null, role);
  }
});

test("a finished reply is told by its last words, and a turn with nothing said is not", () => {
  const said = [entry({ role: "user", text: "how is everyone?" }), entry({ role: "activity" }), entry({ text: "Both fine." })];
  assert.equal(replyAlert(said)?.body, "Both fine.");
  assert.equal(replyAlert([entry({ text: "Earlier." }), entry({ role: "user", text: "and now?" })]), null);
  assert.equal(replyAlert([entry({ role: "user", text: "hi" }), entry({ text: "half", streaming: true })]), null);
});
