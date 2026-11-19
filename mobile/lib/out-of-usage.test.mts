import assert from "node:assert/strict";
import { test } from "node:test";

import { backAt, liveLimitNotice, outOfUsageLabel } from "./out-of-usage.ts";

const at = (y: number, m: number, d: number, h: number, min = 0) => new Date(y, m - 1, d, h, min).getTime();
const now = at(2026, 11, 18, 17, 6);
const time = (ms: number) => new Date(ms).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });

test("a reset later today is its time alone", () => {
  const later = at(2026, 11, 18, 19, 19);
  assert.equal(backAt(later, now), time(later));
  assert.equal(outOfUsageLabel({ resetsAt: later }, now), `Out of usage until ${time(later)}`);
});

test("a reset in the week names its day, and a later one its date", () => {
  const saturday = at(2026, 11, 21, 9);
  assert.equal(backAt(saturday, now), `${new Date(saturday).toLocaleDateString([], { weekday: "short" })} ${time(saturday)}`);
  const december = at(2026, 12, 2, 9);
  assert.equal(backAt(december, now), `${new Date(december).toLocaleDateString([], { month: "short", day: "numeric" })}, ${time(december)}`);
});

test("no reset time is out of usage with no time", () => {
  assert.equal(outOfUsageLabel({}, now), "Out of usage");
  assert.equal(outOfUsageLabel({ resetsAt: 0 }, now), "Out of usage");
});

test("only the newest limit notice holds, and only while the agent is out", () => {
  const rows = [{ id: "a", notice: "limit" }, { id: "b" }, { id: "c", notice: "limit" }, { id: "d" }];
  assert.equal(liveLimitNotice(rows, { resetsAt: 5 }), "c");
  assert.equal(liveLimitNotice(rows, undefined), undefined);
  assert.equal(liveLimitNotice([{ id: "b" }], {}), undefined);
});
