import assert from "node:assert/strict";
import test from "node:test";

import type { Message } from "./protocol.ts";
import { groupTools, runCounts, runFinished } from "./toolgroups.ts";

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

test("a run is finished when no call is running", () => {
  assert.equal(runFinished([call("1"), call("2")]), true);
  assert.equal(runFinished([call("1"), call("2", undefined, "running")]), false);
});

test("a run from an older Mac is told by what kinds of step it took", () => {
  const named = (id: string, name: string): Message => ({ id, sessionId: "s", role: "tool", ts: 1, tool: { name, status: "ok" } });
  assert.equal(runCounts([named("1", "Bash"), named("2", "Bash"), named("3", "Read")]), "Ran 2 commands and read a file");
  assert.equal(runCounts([named("1", "Edit"), named("2", "Grep"), named("3", "Bash")]), "Edited a file, searched once and ran a command");
  const image: Message = { id: "4", sessionId: "s", role: "tool", ts: 1, tool: { name: "Read", summary: "/x/72v33m2m3pjbqp54.jpg", status: "ok" } };
  assert.equal(runCounts([image, named("5", "Bash"), named("6", "Bash")]), "Looked at an image and ran 2 commands");
});

test("a call that read a picture breaks the run around it", () => {
  const image = (id: string): Message => ({
    id, sessionId: "s", role: "tool", ts: 1, tool: { name: "Read", summary: "/tmp/shot.png", status: "ok" },
  });
  const items = groupTools([call("1"), call("2"), image("i"), call("3"), call("4")], (m) => m.tool?.name === "Read");
  assert.deepEqual(
    items.map((item) => (item.kind === "tools" ? `run ${item.calls.length}` : item.message.id)),
    ["run 2", "i", "run 2"],
  );
});

test("pictures read one after another are one run, shown side by side", () => {
  const image = (id: string): Message => ({
    id, sessionId: "s", role: "tool", ts: 1, tool: { name: "Read", summary: `/tmp/${id}.png`, status: "ok" },
  });
  const said: Message = { id: "a", sessionId: "s", role: "assistant", ts: 1, text: "Looking at both screenshots." };
  const items = groupTools(
    [said, image("i1"), image("i2"), call("1"), image("i3"), image("i4"), image("i5")],
    (m) => m.tool?.name === "Read",
  );
  const shape = items.map((item) =>
    item.kind === "tools" ? `${item.pictures ? "pictures" : "run"} ${item.calls.map((c) => c.id).join(",")}` : item.message.id,
  );
  assert.deepEqual(shape, ["a", "pictures i1,i2", "1", "pictures i3,i4,i5"]);
  const first = items[1];
  assert.ok(first.kind === "tools" && first.said === "Looking at both screenshots.");
  assert.equal(runCounts(first.kind === "tools" ? first.calls : []), "Looked at 2 images");
});
