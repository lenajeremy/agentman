import assert from "node:assert/strict";
import { test } from "node:test";

import {
  axisLabel, axisMoney, bucketValue, momentLabel, money, monotonePath, niceAxis, percent, RANGES, rangeTitle,
  rowLabel, sessions, tokens, unitOf,
} from "./usage.ts";
import { decodeDaemonEvent } from "./protocol.ts";

// The same numbers the Mac's usage page writes.
test("numbers are written as the Mac writes them", () => {
  assert.equal(money(13021.987), "$13,021.99");
  assert.equal(money(0), "$0.00");
  assert.equal(money(0.004), "<$0.01");
  assert.equal(axisMoney(600, 200), "$600");
  assert.equal(axisMoney(5, 2.5), "$5.00");

  assert.equal(tokens(0), "0");
  assert.equal(tokens(950), "950");
  assert.equal(tokens(12_600), "12.6K");
  assert.equal(tokens(410_000_000), "410M");
  assert.equal(tokens(2_301_000_000), "2.30B");
  assert.equal(tokens(999_600_000), "1.00B");
  assert.equal(tokens(999_400_000), "999M");

  assert.equal(percent(0), "0%");
  assert.equal(percent(0.0003), "<0.1%");
  assert.equal(percent(0.077), "7.7%");
  assert.equal(sessions(1), "1 session");
  assert.equal(sessions(1694), "1,694 sessions");
});

test("ranges and their buckets are named", () => {
  assert.deepEqual(RANGES.map(rangeTitle), ["24h", "7d", "30d", "90d", "6m", "1y", "All"]);
  assert.equal(unitOf({ unit: "week" }), "week");
  assert.equal(unitOf({ hourly: true }), "hour", "a Mac that predates units");
  assert.equal(unitOf({}), "day");
  const monday = new Date(2026, 9, 26);
  assert.equal(momentLabel("week", monday, "en-GB"), "Week of 26 Oct");
  assert.equal(rowLabel("week", monday, "en-GB"), "Week of 26 Oct 2026");
  assert.equal(momentLabel("month", monday, "en-GB"), "October 2026");
  assert.equal(axisLabel("day", monday, 89 * 86_400_000, "en-GB"), "26 OCT");
  assert.equal(axisLabel("week", monday, 364 * 86_400_000, "en-GB"), "OCT 2026");
});

// Three or four round steps, up to past the highest point.
test("the chart's axis rounds up to a step", () => {
  assert.deepEqual(niceAxis(520, "cost"), { top: 600, step: 200 });
  assert.deepEqual(niceAxis(1_600_000_000, "tokens"), { top: 2_000_000_000, step: 1_000_000_000 });
  assert.deepEqual(niceAxis(0, "cost"), { top: 0.01, step: 0.005 });
  assert.equal(bucketValue({ start: 0, cost: { claude: 2 } }, "claude", "cost"), 2);
  assert.equal(bucketValue({ start: 0 }, "codex", "tokens"), 0);
});

// A quiet day between busy ones stays on the baseline: the curve never
// passes beyond the points it joins.
test("the line never overshoots its points", () => {
  const points = [{ x: 0, y: 100 }, { x: 10, y: 0 }, { x: 20, y: 100 }, { x: 30, y: 100 }, { x: 40, y: 20 }];
  const path = monotonePath(points);
  assert.match(path, /^M0,100C/);
  const numbers = path.replace(/[MC]/g, ",").split(",").filter(Boolean).map(Number);
  const ys = numbers.filter((_, index) => index % 2 === 1);
  assert.ok(Math.max(...ys) <= 100 && Math.min(...ys) >= 0, `control points stay within the data: ${ys}`);
  assert.equal(monotonePath([]), "");
  assert.equal(monotonePath([{ x: 1, y: 2 }]), "M1,2");
});

test("a usage event is read, and a malformed one is not", () => {
  const usage = {
    days: 7, unit: "day", sessions: 2,
    tokens: { input: 1, cacheRead: 2, cacheWrite: 3, output: 4 },
    cost: { input: 0.1, cacheRead: 0.2, cacheWrite: 0.3, output: 0.4 },
    cacheSavings: 1.5,
    agents: [{ kind: "claude", sessions: 2, tokens: { input: 1, cacheRead: 2, cacheWrite: 3, output: 4 }, cost: 1 }],
    buckets: [{ start: 1761868800000, cost: { claude: 1 }, tokens: { claude: 10 } }, { start: 1761955200000 }],
    models: [{ model: "claude-opus-5-5", kind: "claude", tokens: { input: 1, cacheRead: 2, cacheWrite: 3, output: 4 }, cost: 1, priced: true }],
  };
  assert.equal(decodeDaemonEvent({ type: "usage", usage })?.usage?.sessions, 2);
  assert.equal(decodeDaemonEvent({ type: "usage", usage: { ...usage, tokens: { input: "1" } } }), null);
  assert.equal(decodeDaemonEvent({ type: "usage" }), null);
});
