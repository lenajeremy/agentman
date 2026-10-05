import assert from "node:assert/strict";
import test from "node:test";

import { defaultScopeWarning, switchChoices, switchFailure } from "./switching.ts";

const claude = {
  mode: "default", modes: ["default", "accept-edits", "plan"],
  model: "opus", models: ["opus", "sonnet"], modelScope: "session" as const,
};

test("a session offers the modes and models its agent listed, with the current one", () => {
  assert.deepEqual(switchChoices(claude, "mode"), { values: ["default", "accept-edits", "plan"], current: "default" });
  assert.deepEqual(switchChoices(claude, "model"), { values: ["opus", "sonnet"], current: "opus" });
});

test("nothing listed is nothing to switch", () => {
  assert.equal(switchChoices({}, "mode"), null);
  assert.equal(switchChoices({ modes: [] }, "mode"), null);
  assert.equal(switchChoices({ models: [] , modelScope: "session" }, "model"), null);
});

test("models without a scope are not offered, because nobody could be told what changes", () => {
  assert.equal(switchChoices({ ...claude, modelScope: undefined }, "model"), null);
  assert.ok(switchChoices({ ...claude, modelScope: undefined }, "mode"));
});

test("a model switch that changes the Mac's default is confirmed, naming the agent", () => {
  assert.equal(
    defaultScopeWarning({ modelScope: "default" }, "model", "Antigravity", "gemini-3-pro"),
    "gemini-3-pro also becomes the default model for new Antigravity sessions on your Mac.",
  );
  assert.equal(defaultScopeWarning({ modelScope: "session" }, "model", "Kiro", "claude-sonnet-4.5"), null);
  assert.equal(defaultScopeWarning({ modelScope: "default" }, "mode", "Cursor", "plan"), null);
});

test("a failure names the switch and the Mac's reason", () => {
  assert.equal(
    switchFailure("mode", "plan", "daemon: answer the pending question before switching"),
    "Couldn’t switch the mode to plan: answer the pending question before switching",
  );
  assert.equal(switchFailure("model", "sonnet", ""), "Couldn’t switch the model to sonnet.");
});
