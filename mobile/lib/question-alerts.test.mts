import assert from "node:assert/strict";
import test from "node:test";

import {
  reconcileQuestionAlerts,
  sessionNeedsAnswer,
  updateQuestionAlerts,
} from "./question-alerts.ts";

const question = { prompt: "Which database?", options: [] };

test("a newly blocked session alerts exactly once across repeated snapshots", () => {
  const sessions = [
    { id: "claude:1", state: "waiting_input" as const, question },
    { id: "codex:2", state: "busy" as const },
  ];
  const first = reconcileQuestionAlerts(new Set(), sessions);
  assert.deepEqual(first.newlyPending.map((session) => session.id), ["claude:1"]);

  const repeated = reconcileQuestionAlerts(first.announced, sessions);
  assert.deepEqual(repeated.newlyPending, []);
});

test("progressing through one form does not send another alert", () => {
  const current = new Set(["opencode:1"]);
  const next = updateQuestionAlerts(current, {
    id: "opencode:1",
    state: "waiting_input" as const,
    question: { prompt: "Question 2 of 3", options: [] },
  });
  assert.equal(next.newlyPending, undefined);
  assert.deepEqual([...next.announced], ["opencode:1"]);
});

test("resolving a question arms the next independent question", () => {
  const resolved = updateQuestionAlerts(new Set(["codex:1"]), {
    id: "codex:1",
    state: "busy" as const,
  });
  assert.equal(resolved.announced.has("codex:1"), false);

  const blockedAgain = updateQuestionAlerts(resolved.announced, {
    id: "codex:1",
    state: "waiting_input" as const,
    question,
  });
  assert.equal(blockedAgain.newlyPending?.id, "codex:1");
});

test("waiting_input still alerts when an older daemon has no parsed question", () => {
  const result = reconcileQuestionAlerts(new Set(), [
    { id: "claude:legacy", state: "waiting_input" as const },
  ]);
  assert.deepEqual(result.newlyPending.map((session) => session.id), ["claude:legacy"]);
});

test("a completion alert is suppressed while the session needs an answer", () => {
  assert.equal(sessionNeedsAnswer({ id: "claude:1", state: "waiting_input" }), true);
  assert.equal(
    sessionNeedsAnswer({ id: "claude:1", state: "idle", question }),
    true,
  );
  assert.equal(sessionNeedsAnswer({ id: "claude:1", state: "idle" }), false);
});
