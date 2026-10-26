import assert from "node:assert/strict";
import { test } from "node:test";

import { agentSpeaks, DEFAULT_NOTIFY_PREFS, parseNotifyPrefs, shouldAlertOnPhone } from "./notification-prefs.ts";

test("stored preferences read back, and anything unreadable means the defaults", () => {
  assert.deepEqual(parseNotifyPrefs(JSON.stringify({ finished: false, needsYou: true, agentman: false, agents: true })), {
    finished: false,
    needsYou: true,
    agentman: false,
    agents: true,
  });
  assert.deepEqual(parseNotifyPrefs(null), DEFAULT_NOTIFY_PREFS);
  assert.deepEqual(parseNotifyPrefs("not json"), DEFAULT_NOTIFY_PREFS);
  // One field missing or mistyped keeps its default, not the whole set: a
  // choice saved before Agentman's existed hears Agentman, and leaves the
  // agents to it.
  assert.deepEqual(parseNotifyPrefs(JSON.stringify({ finished: false, needsYou: "no" })), {
    finished: false,
    needsYou: true,
    agentman: true,
    agents: false,
  });
});

test("while Agentman is on, an agent speaks for itself only when you asked", () => {
  const away = { appActive: false, pushActive: false };
  assert.equal(agentSpeaks(DEFAULT_NOTIFY_PREFS, { agentmanOn: true, asked: false }), false);
  assert.equal(agentSpeaks(DEFAULT_NOTIFY_PREFS, { agentmanOn: true, asked: true }), true);
  assert.equal(agentSpeaks({ ...DEFAULT_NOTIFY_PREFS, agents: true }, { agentmanOn: true, asked: false }), true);
  assert.equal(agentSpeaks(DEFAULT_NOTIFY_PREFS, { agentmanOn: false, asked: false }), true);
  // Agentman itself is a kind of its own, on by default.
  assert.equal(shouldAlertOnPhone("agentman", DEFAULT_NOTIFY_PREFS, away), true);
  assert.equal(shouldAlertOnPhone("agentman", { ...DEFAULT_NOTIFY_PREFS, agentman: false }, away), false);
});

test("the app never alerts while it is open", () => {
  const everything = { finished: true, needsYou: true, agentman: true, agents: true };
  for (const kind of ["finished", "needsYou", "agentman"] as const) {
    assert.equal(shouldAlertOnPhone(kind, everything, { appActive: true, pushActive: false }), false);
  }
});

test("the app leaves alerts to the Mac once push is working, so none arrives twice", () => {
  assert.equal(
    shouldAlertOnPhone("needsYou", DEFAULT_NOTIFY_PREFS, { appActive: false, pushActive: true }),
    false,
  );
});

test("away from the app, only the kinds you kept alert", () => {
  const onlyNeedsYou = { ...DEFAULT_NOTIFY_PREFS, finished: false };
  const away = { appActive: false, pushActive: false };
  assert.equal(shouldAlertOnPhone("needsYou", onlyNeedsYou, away), true);
  assert.equal(shouldAlertOnPhone("finished", onlyNeedsYou, away), false);
});
