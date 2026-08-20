import assert from "node:assert/strict";
import { test } from "node:test";

import { canDismiss, dismiss, isHidden, prune } from "./dismissRules.ts";
import type { Session } from "./protocol.ts";

/**
 * Run with `npm test` in mobile/ — Node's own test runner, no framework.
 *
 * These rules decide whether an agent appears on the status board at all, and
 * the dangerous direction is hiding something that needs you. Each test below
 * names the consequence rather than the assertion.
 */

function session(over: Partial<Session> = {}): Session {
  return {
    id: "claude:s1",
    kind: "claude",
    nativeId: "s1",
    name: "agentman",
    cwd: "/Users/me/code",
    state: "idle",
    inject: "tmux",
    startedAt: 1_000,
    lastActivityAt: 5_000,
    ...over,
  };
}

test("a swiped row stays hidden while nothing happens", () => {
  const idle = session();
  const dismissals = dismiss(idle, {});
  assert.equal(isHidden(idle, dismissals), true);
  // Repeated discovery sweeps report the same session unchanged; none of them
  // should bring it back.
  assert.equal(isHidden(session(), dismissals), true);
});

test("new activity brings it back", () => {
  const dismissals = dismiss(session({ lastActivityAt: 5_000 }), {});
  const active = session({ lastActivityAt: 5_001 });
  assert.equal(
    isHidden(active, dismissals),
    false,
    "an agent that did something stayed hidden, so the user never learned it ran",
  );
});

test("going busy brings it back even without new activity", () => {
  // The two are updated separately, and a session can flip to busy on the same
  // sweep that its timestamp is still the old one.
  const dismissals = dismiss(session(), {});
  assert.equal(isHidden(session({ state: "busy" }), dismissals), false);
});

test("a question always brings it back", () => {
  // The worst possible failure: an agent blocked on a permission prompt,
  // invisible because it was swiped away while idle a minute earlier.
  const dismissals = dismiss(session(), {});
  const blocked = session({
    state: "waiting_input",
    question: { prompt: "Run npm test?", options: [{ key: "1", label: "Yes" }] },
  });
  assert.equal(isHidden(blocked, dismissals), false);
});

test("a parsed question wins even when discovery still reports idle", () => {
  const dismissals = dismiss(session(), {});
  const blocked = session({
    state: "idle",
    question: { prompt: "Run npm test?", options: [{ key: "1", label: "Yes" }] },
  });
  assert.equal(
    isHidden(blocked, dismissals),
    false,
    "the alert path treats idle plus question as blocked, so visibility must agree",
  );
  assert.equal(canDismiss(blocked), false);
});

test("only idle sessions can be swiped away", () => {
  assert.equal(canDismiss(session({ state: "idle" })), true);
  assert.equal(canDismiss(session({ state: "busy" })), false);
  assert.equal(
    canDismiss(session({ state: "waiting_input" })),
    false,
    "a blocked agent could be hidden, which is how one sits unanswered for an hour",
  );
});

test("hiding one session does not hide another", () => {
  const first = session({ id: "claude:s1" });
  const second = session({ id: "codex:s2" });
  const dismissals = dismiss(first, {});
  assert.equal(isHidden(first, dismissals), true);
  assert.equal(isHidden(second, dismissals), false);
});

test("prune keeps entries that are still hiding something", () => {
  const idle = session();
  const dismissals = dismiss(idle, {});
  assert.deepEqual(prune([idle], dismissals), dismissals);
});

test("prune drops an entry once its session is back", () => {
  // Otherwise the entry lingers and would hide the session again the next time
  // it went quiet — without the user ever swiping it.
  const dismissals = dismiss(session({ lastActivityAt: 5_000 }), {});
  const pruned = prune([session({ lastActivityAt: 9_000 })], dismissals);
  assert.deepEqual(pruned, {}, "a stale dismissal survived and would hide the session again");
});

test("prune drops entries for sessions that no longer exist", () => {
  // This is what bounds storage. A session id that returns later is a session
  // that was started again, and should be visible.
  const dismissals = dismiss(session({ id: "claude:gone" }), {});
  assert.deepEqual(prune([], dismissals), {});
});

test("an unknown session is never hidden", () => {
  assert.equal(isHidden(session(), {}), false);
});

test("a dismissal from the future does not hide a fresh session forever", () => {
  // Clocks disagree: the daemon stamps lastActivityAt, the phone reads it. If a
  // session's timestamp ever ran ahead of what was recorded, the row must still
  // come back the moment real activity passes it.
  const dismissals = dismiss(session({ lastActivityAt: 10_000 }), {});
  assert.equal(isHidden(session({ lastActivityAt: 10_001 }), dismissals), false);
});
