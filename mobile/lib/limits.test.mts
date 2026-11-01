import assert from "node:assert/strict";
import { test } from "node:test";

import {
  checkedLabel, elapsed, evenLeft, leftPercent, orderedWindows, pace, resetsLabel, span,
} from "./limits.ts";
import { decodeDaemonEvent, type LimitWindow } from "./protocol.ts";

const now = 1_800_000_000_000;
const hour = 3_600_000;

function window(used: number, hoursLeft: number): LimitWindow {
  return { id: "w", kind: "session", label: "Session", used, resetsAt: now + hoursLeft * hour, minutes: 300 };
}

// Pace as T3 Code has it, and the Mac shows it: five points either side of
// how far through the window the plan is.
test("pace is how fast the plan is spent against the window", () => {
  assert.equal(elapsed(window(0, 2.5), now), 0.5);
  assert.equal(evenLeft(window(0, 2.5), now), 50);
  assert.equal(pace(window(56, 2.5), now), "ahead");
  assert.equal(pace(window(54, 2.5), now), "on");
  assert.equal(pace(window(44, 2.5), now), "under");
  assert.equal(pace({ ...window(44, 2.5), minutes: undefined }, now), null, "no length, no pace");
  assert.equal(pace({ id: "x", kind: "monthly", label: "Extra usage", used: 10 }, now), null, "no reset, no pace");
  assert.equal(elapsed(window(0, 9), now), 0, "a reset further off than the window is the window's start");
});

test("what is left and when it resets are written as the Mac writes them", () => {
  assert.equal(leftPercent({ used: 37.6 }), 62);
  assert.equal(leftPercent({ used: 140 }), 0);
  assert.equal(leftPercent({ used: -3 }), 100);
  assert.equal(resetsLabel(window(0, 3.2), now), "Resets in 3h 12m");
  assert.equal(resetsLabel(window(0, 98), now), "Resets in 4d 2h");
  assert.equal(resetsLabel(window(0, 0.2), now), "Resets in 12m");
  assert.equal(resetsLabel(window(0, -1), now), "Resets now");
  assert.equal(resetsLabel({ id: "x", kind: "monthly", label: "Extra usage", used: 10 }, now), null);
  assert.equal(span(59_000), "0m");

  assert.equal(checkedLabel({ checkedAt: now - 180_000 }, now), "Checked 3m ago");
  assert.equal(checkedLabel({ checkedAt: now, lastTurn: true }, now), "As of its last reply, just now");
  assert.equal(checkedLabel({ checkedAt: now - 2 * hour, lastTurn: true }, now), "As of its last reply, 2h 0m ago");
});

test("the five hours come first, then the weeks, then the months", () => {
  const windows: LimitWindow[] = [
    { id: "extra", kind: "monthly", label: "Extra usage", used: 1 },
    { id: "week", kind: "weekly", label: "Weekly", used: 1 },
    { id: "five", kind: "session", label: "Session", used: 1 },
    { id: "opus", kind: "weekly", label: "Weekly · Opus", used: 1 },
    { id: "odd", kind: "other", label: "Quota", used: 1 },
  ];
  assert.deepEqual(orderedWindows(windows).map((w) => w.id), ["five", "week", "opus", "extra", "odd"]);
});

// The Mac's answer is let through the event whitelist and checked.
test("a limits event is read, and a malformed one dropped", () => {
  const event = decodeDaemonEvent({
    type: "limits",
    limits: {
      agents: [
        {
          kind: "claude", plan: "Max", checkedAt: now,
          windows: [
            { id: "five_hour", kind: "session", label: "Session", used: 38, resetsAt: now + hour, minutes: 300 },
            { id: "extra_usage", kind: "monthly", label: "Extra usage", used: 25 },
          ],
        },
        { kind: "codex", checkedAt: now, lastTurn: true, windows: null, problem: "Codex hasn't said." },
      ],
    },
  });
  assert.ok(event?.limits);
  assert.equal(event.limits.agents.length, 2);
  assert.equal(event.limits.agents[0].windows?.[0].resetsAt, now + hour);
  assert.equal(event.limits.agents[1].lastTurn, true);

  assert.ok(decodeDaemonEvent({ type: "limits", limits: { agents: [] } }), "no agent with limits is an answer");
  assert.equal(decodeDaemonEvent({ type: "limits" }), null);
  assert.equal(decodeDaemonEvent({ type: "limits", limits: { agents: [{ kind: "claude" }] } }), null, "no checkedAt");
  assert.equal(decodeDaemonEvent({
    type: "limits",
    limits: { agents: [{ kind: "claude", checkedAt: now, windows: [{ id: "a", kind: "session", label: "Session", used: "38" }] }] },
  }), null, "a share that isn't a number");
});
