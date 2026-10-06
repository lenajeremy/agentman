import assert from "node:assert/strict";
import { test } from "node:test";

import { DEFAULT_NOTIFY_PREFS, parseNotifyPrefs, shouldAlertOnPhone } from "./notification-prefs.ts";

test("stored preferences read back, and anything unreadable means everything on", () => {
  assert.deepEqual(parseNotifyPrefs(JSON.stringify({ finished: false, needsYou: true })), {
    finished: false,
    needsYou: true,
  });
  assert.deepEqual(parseNotifyPrefs(null), DEFAULT_NOTIFY_PREFS);
  assert.deepEqual(parseNotifyPrefs("not json"), DEFAULT_NOTIFY_PREFS);
  // One field missing or mistyped keeps its default, not the whole set.
  assert.deepEqual(parseNotifyPrefs(JSON.stringify({ finished: false, needsYou: "no" })), {
    finished: false,
    needsYou: true,
  });
});

test("the app never alerts while it is open", () => {
  const everything = { finished: true, needsYou: true };
  for (const kind of ["finished", "needsYou"] as const) {
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
  const onlyNeedsYou = { finished: false, needsYou: true };
  const away = { appActive: false, pushActive: false };
  assert.equal(shouldAlertOnPhone("needsYou", onlyNeedsYou, away), true);
  assert.equal(shouldAlertOnPhone("finished", onlyNeedsYou, away), false);
});
