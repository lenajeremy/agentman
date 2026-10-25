import assert from "node:assert/strict";
import test from "node:test";

import {
  applyAgentActionResult,
  applyPendingSendResult,
  reconcileCursorSendEchoes,
  clearQueuedForSessionTransitions,
  clearQueuedForTurn,
  questionIdentity,
  reconcileAgentActions,
  settleQueuedEchoes,
  isQueued,
  holdsMessages,
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

test("Cursor send stays visible until its new transcript row arrives", () => {
  const pending = [{
    clientId: "send-1", sessionId: "cursor-cli:pane:x", text: "hello",
    status: "sending" as const, retainUntilTranscript: true,
    knownUserMessageIds: ["old"],
  }];
  const delivered = applyPendingSendResult(pending, {
    clientId: "send-1", status: "delivered",
  });
  assert.equal(delivered[0].status, "delivered");
  const oldRow = {
    id: "old", sessionId: pending[0].sessionId, role: "user" as const,
    text: "hello", ts: 1,
  };
  assert.equal(reconcileCursorSendEchoes(delivered, pending[0].sessionId, [oldRow]).length, 1);
  assert.equal(reconcileCursorSendEchoes(delivered, pending[0].sessionId, [
    oldRow, { ...oldRow, id: "new", ts: 2 },
  ]).length, 0);
});

test("repeated Cursor sends each need a distinct transcript row", () => {
  const sessionId = "cursor-cli:pane:x";
  const pending = ["a", "b"].map((clientId) => ({
    clientId, sessionId, text: "again", status: "delivered" as const,
    retainUntilTranscript: true,
  }));
  const row = { id: "new", sessionId, role: "user" as const, text: "again", ts: 1 };
  assert.deepEqual(
    reconcileCursorSendEchoes(pending, sessionId, [row]).map((item) => item.clientId),
    ["b"],
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

// An image-only send has no text to match its transcript row by, and Cursor
// records the row with the image paths taken out — empty, or "[image]". The
// echo used to stay on screen forever. Once the Mac has delivered it, the
// next new row from the user is that message.
test("an image-only Cursor send settles when its row arrives", () => {
  const sessionId = "cursor-cli:pane:x";
  const old = { id: "old", sessionId, role: "user" as const, text: "earlier", ts: 1 };
  const pending = [{
    clientId: "send-1", sessionId, text: "", status: "sending" as const,
    retainUntilTranscript: true, knownUserMessageIds: ["old"],
  }];
  // Not yet delivered: a new row could be something typed at the terminal.
  const fresh = { id: "new", sessionId, role: "user" as const, text: "[image]", ts: 2 };
  assert.equal(reconcileCursorSendEchoes(pending, sessionId, [old, fresh]).length, 1);

  const delivered = applyPendingSendResult(pending, { clientId: "send-1", status: "delivered" });
  assert.equal(reconcileCursorSendEchoes(delivered, sessionId, [old]).length, 1);
  assert.equal(reconcileCursorSendEchoes(delivered, sessionId, [old, fresh]).length, 0);
  assert.equal(reconcileCursorSendEchoes(delivered, sessionId, [old, { ...fresh, text: "" }]).length, 0);
});

test("a message sent while Claude works stays until Claude's own queue shows it", () => {
  const sent = [{
    clientId: "send-q", sessionId: "claude:s1", text: "also update the README",
    status: "delivered" as const, retainUntilTranscript: true,
  }];
  // Delivered, but the Mac has not reported the queue yet: keep the bubble.
  assert.equal(settleQueuedEchoes(sent, [session()]).length, 1);
  // Listed in the queue: the queued row takes over, so the echo goes.
  assert.equal(settleQueuedEchoes(sent, [session({ queued: ["also update the README"] })]).length, 0);
  // Still waiting on a question: kept.
  assert.equal(settleQueuedEchoes(sent, [session({ state: "waiting_input" })]).length, 1);
  // The turn ended, so the queue was read: nothing left to wait for.
  assert.equal(settleQueuedEchoes(sent, [session({ state: "idle" })]).length, 0);
});

test("settling queued echoes leaves everything else alone", () => {
  const items = [
    { clientId: "a", sessionId: "claude:s1", text: "x", status: "sending" as const, retainUntilTranscript: true },
    { clientId: "b", sessionId: "claude:s1", text: "x", status: "delivered" as const },
    { clientId: "c", sessionId: "cursor-cli:c", text: "x", status: "delivered" as const, retainUntilTranscript: true },
    { clientId: "d", sessionId: "claude:gone", text: "x", status: "delivered" as const, retainUntilTranscript: true },
  ];
  const sessions = [session({ state: "idle", queued: ["x"] }), session({ id: "cursor-cli:c", kind: "cursor-cli", state: "idle" })];
  assert.deepEqual(settleQueuedEchoes(items, sessions).map((item) => item.clientId), ["a", "b", "c", "d"]);
});

test("a queued message matches the text sent, with the agent's additions around it", () => {
  assert.equal(isQueued(session({ queued: ["  fix it  "] }), "fix it"), true);
  assert.equal(isQueued(session({ queued: ["fix it\n[Image #1]"] }), "fix it"), true);
  assert.equal(isQueued(session({ queued: ["something else"] }), "fix it"), false);
  assert.equal(isQueued(session({ queued: ["anything"] }), "   "), false);
  assert.equal(isQueued(session(), "fix it"), false);
});

test("Send now stays in progress until Claude has nothing left waiting", () => {
  const action: PendingAgentAction = { clientId: "now-1", sessionId: "claude:s1", kind: "send_queued", status: "delivered" };
  assert.equal(reconcileAgentActions([action], [session({ queued: ["later"] })]).length, 1);
  assert.equal(reconcileAgentActions([action], [session()]).length, 0);
  // A failure is kept so it can be said, until the next attempt replaces it.
  assert.equal(reconcileAgentActions([{ ...action, status: "failed" }], [session()]).length, 1);
  assert.equal(reconcileAgentActions([action], []).length, 0);
});

test("every agent that holds messages hands its echo over the same way", () => {
  for (const kind of ["claude", "antigravity", "codex", "opencode"] as const) {
    assert.equal(holdsMessages(kind), true, kind);
    const id = `${kind}:s1`;
    const sent = [{ clientId: "q", sessionId: id, text: "later", status: "delivered" as const, retainUntilTranscript: true }];
    assert.equal(settleQueuedEchoes(sent, [session({ id, kind })]).length, 1, `${kind} while working`);
    assert.equal(settleQueuedEchoes(sent, [session({ id, kind, queued: ["later"] })]).length, 0, `${kind} once queued`);
    assert.equal(settleQueuedEchoes(sent, [session({ id, kind, state: "idle" })]).length, 0, `${kind} once idle`);
  }
  // Cursor keeps its own rule: the transcript settles it, not the queue.
  assert.equal(holdsMessages("cursor-cli"), false);
});
