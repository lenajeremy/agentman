import assert from "node:assert/strict";
import { test } from "node:test";

import {
  initialPushRegistration,
  pushRegistrationAccepted,
  pushRegistrationRejected,
  pushRegistrationSent,
} from "./push-registration.ts";

// The app stops scheduling its own alerts once the Mac sends them by push.
// It used to stop as soon as it had asked, before the Mac had the token —
// or while the Mac was offline and never saw the request — and the phone
// then got no alerts at all.
test("local alerts stay on until the Mac has kept the token", () => {
  let state = pushRegistrationSent(initialPushRegistration, "r1");
  assert.equal(state.active, false);
  state = pushRegistrationAccepted(state, "r1");
  assert.equal(state.active, true);
});

test("a refusal leaves local alerts on", () => {
  let state = pushRegistrationSent(initialPushRegistration, "r1");
  state = pushRegistrationRejected(state, "r1");
  assert.equal(state.active, false);
  // A late acceptance of the refused request changes nothing.
  assert.equal(pushRegistrationAccepted(state, "r1").active, false);
});

// Registering again, when the Mac comes back, keeps push working meanwhile:
// it was working, and a reconnect is not evidence that it stopped.
test("registering again keeps an accepted token active until refused", () => {
  let state = pushRegistrationAccepted(pushRegistrationSent(initialPushRegistration, "r1"), "r1");
  state = pushRegistrationSent(state, "r2");
  assert.equal(state.active, true);
  // Only the latest request decides.
  assert.equal(pushRegistrationRejected(state, "r1").active, true);
  assert.equal(pushRegistrationRejected(state, "r2").active, false);
});

test("an unsent request changes nothing", () => {
  assert.deepEqual(pushRegistrationSent(initialPushRegistration, null), initialPushRegistration);
});
