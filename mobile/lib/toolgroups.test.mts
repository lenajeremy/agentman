import assert from "node:assert/strict";
import test from "node:test";

import type { Message } from "./protocol.ts";
import { groupTools, runFinished, runFromSteps } from "./toolgroups.ts";

const said = (id: string, text: string): Message => ({ id, sessionId: "s", role: "assistant", ts: 1, text });
const call = (id: string, title?: string, status: "running" | "ok" = "ok"): Message => ({
  id,
  sessionId: "s",
  role: "tool",
  ts: 1,
  tool: { name: "Bash", summary: "x", status, ...(title ? { title } : {}) },
});

test("calls in a row make a run, with what the agent said before it", () => {
  const items = groupTools([said("a", "Now the phone."), call("1"), call("2"), said("b", "Done."), call("3")]);
  assert.deepEqual(
    items.map((item) => (item.kind === "tools" ? `run ${item.calls.length} after "${item.said}"` : item.message.id)),
    ["a", 'run 2 after "Now the phone."', "b", "3"],
  );
});

test("a run is told by its steps' own words", () => {
  assert.equal(runFromSteps([call("1", "Rebuilt the daemon"), call("2", "Paired the phone")]), "Rebuilt the daemon, then paired the phone");
  assert.equal(
    runFromSteps([call("1", "Rebuilt the daemon"), call("2", "Paired the phone"), call("3", "Took a screenshot")]),
    "Rebuilt the daemon, paired the phone and 1 more step",
  );
  assert.equal(runFromSteps([call("1"), call("2", "Paired the phone")]), undefined);
});

test("a run is finished when no call is running", () => {
  assert.equal(runFinished([call("1"), call("2")]), true);
  assert.equal(runFinished([call("1"), call("2", undefined, "running")]), false);
});
