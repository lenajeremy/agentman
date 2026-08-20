import assert from "node:assert/strict";
import test from "node:test";

import {
  applyAgentActionResult,
  applyPendingSendResult,
  clearQueuedForSessionTransitions,
  clearQueuedForTurn,
  questionIdentity,
  reconcileAgentActions,
  upsertAgentAction,
  type PendingAgentAction,
} from "./action-state.ts";
import type { Question, Session } from "./protocol.ts";

function session(over: Partial<Session> = {}): Session {
  return {
    id: "claude:s1",
    kind: "claude",
    nativeId: "s1",
    name: "agentman",
    cwd: "/work",
    state: "busy",
    inject: "tmux",
    startedAt: 1,
    lastActivityAt: 2,
    ...over,
  };
}

const question: Question = {
  id: "question-1",
  prompt: "Choose a path",
  options: [{ key: "1", label: "Local", preview: "LOCAL" }],
};

test("a delivered message leaves pending state instead of leaking invisibly", () => {
  const pending = [{ clientId: "send-1", sessionId: "s", text: "hello", status: "sending" as const }];
  assert.deepEqual(
    applyPendingSendResult(pending, { clientId: "send-1", status: "delivered" }),
    [],
  );
});

test("queued and failed message results remain honest and visible", () => {
  const pending = [{ clientId: "send-1", sessionId: "s", text: "hello", status: "sending" as const }];
  assert.equal(
    applyPendingSendResult(pending, { clientId: "send-1", status: "queued" })[0].status,
    "queued",
  );
  const failed = applyPendingSendResult(pending, {
    clientId: "send-1",
    status: "failed",
    error: "offline",
  });
  assert.equal(failed[0].error, "offline");
});

test("turn completion removes only that session's queued messages", () => {
  const pending = [
    { clientId: "a", sessionId: "s1", status: "queued" as const },
    { clientId: "b", sessionId: "s2", status: "queued" as const },
    { clientId: "c", sessionId: "s1", status: "failed" as const },
  ];
  assert.deepEqual(clearQueuedForTurn(pending, "s1").map((item) => item.clientId), ["b", "c"]);
});

test("a busy-to-idle snapshot clears a queued handoff after reconnect", () => {
  const pending = [{ clientId: "a", sessionId: "claude:s1", status: "queued" as const }];
  assert.deepEqual(
    clearQueuedForSessionTransitions(
      pending,
      [session({ state: "busy" })],
      [session({ state: "idle" })],
    ),
    [],
  );
  assert.equal(
    clearQueuedForSessionTransitions(
      pending,
      [session({ state: "busy" })],
      [session({ state: "waiting_input" })],
    ).length,
    1,
    "a question is not proof that the queued message was handed off",
  );
  assert.equal(
    clearQueuedForSessionTransitions(pending, [session()], []).length,
    1,
    "a partial discovery snapshot is not proof that the queued session ended",
  );
});

test("answer results stay attached only to the exact live question", () => {
  const action: PendingAgentAction = {
    clientId: "answer-1",
    sessionId: "claude:s1",
    kind: "answer",
    status: "sending",
    questionIdentity: questionIdentity(question),
  };
  const delivered = applyAgentActionResult([action], {
    clientId: "answer-1",
    status: "delivered",
  });
  assert.equal(delivered[0].status, "delivered");
  assert.equal(
    reconcileAgentActions(delivered, [session({ state: "waiting_input", question })]).length,
    1,
  );
  assert.equal(
    reconcileAgentActions(delivered, [
      session({
        state: "waiting_input",
        question: {
          ...question,
          options: [{ ...question.options[0], selected: true, checked: true }],
        },
      }),
    ]).length,
    1,
    "terminal focus and checkmarks can change while the same answer is in flight",
  );
  assert.deepEqual(
    reconcileAgentActions(delivered, [
      session({
        state: "waiting_input",
        question: { ...question, prompt: "A later question" },
      }),
    ]),
    [],
  );
  assert.deepEqual(
    reconcileAgentActions(delivered, [
      session({
        state: "waiting_input",
        question: { ...question, id: "new-question" },
      }),
    ]),
    [],
    "a server question id makes otherwise identical prompts distinct",
  );
});

test("interrupt feedback clears when the turn is no longer busy", () => {
  const action: PendingAgentAction = {
    clientId: "interrupt-1",
    sessionId: "claude:s1",
    kind: "interrupt",
    status: "failed",
    error: "did not land",
  };
  assert.equal(reconcileAgentActions([action], [session({ state: "busy" })]).length, 1);
  assert.deepEqual(reconcileAgentActions([action], [session({ state: "idle" })]), []);
});

test("retry replaces the prior action instead of creating duplicate state", () => {
  const first: PendingAgentAction = {
    clientId: "answer-1",
    sessionId: "claude:s1",
    kind: "answer",
    status: "failed",
  };
  const retry = { ...first, clientId: "answer-2", status: "sending" as const };
  assert.deepEqual(upsertAgentAction([first], retry), [retry]);
});
