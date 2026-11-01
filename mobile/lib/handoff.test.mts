import assert from "node:assert/strict";
import { test } from "node:test";

import { handoffError, handoffItem } from "./handoff.ts";

test("a session Agentman runs opens in a terminal, one in its pane is taken over", () => {
  assert.deepEqual(handoffItem({ handoff: "terminal", state: "idle" }, false, true),
    { to: "terminal", label: "Open in terminal", icon: "terminal", disabled: false });
  assert.deepEqual(handoffItem({ handoff: "agentman", state: "idle" }, false, true),
    { to: "agentman", label: "Take over", icon: "log-in", disabled: false });
});

test("only between turns, one move at a time, and with the Mac in reach", () => {
  assert.equal(handoffItem({ handoff: "terminal", state: "busy" }, false, true)?.disabled, true);
  assert.equal(handoffItem({ handoff: "agentman", state: "waiting_input", question: {} }, false, true)?.disabled, true);
  assert.equal(handoffItem({ handoff: "terminal", state: "idle" }, true, true)?.disabled, true);
  assert.equal(handoffItem({ handoff: "terminal", state: "idle" }, false, false)?.disabled, true);
});

test("a session that cannot move, or names a move this phone does not know, has no row", () => {
  assert.equal(handoffItem({ state: "idle" }, false, true), null);
  assert.equal(handoffItem({ handoff: "sideways", state: "idle" }, false, true), null);
});

test("a refusal reads without the daemon's prefix", () => {
  assert.equal(handoffError("source: Kiro is in the middle of a turn."), "Kiro is in the middle of a turn.");
  assert.equal(handoffError("daemon: this session is not running"), "this session is not running");
  assert.equal(handoffError("unsupported request type: handoff_session"),
    "Update agentman on your Mac to move sessions from here.");
});
