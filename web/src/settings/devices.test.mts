// node --test web/src/settings/devices.test.mts (Node strips the types).
import { test } from "node:test";
import assert from "node:assert/strict";
import { connectedIds, deviceDetail, deviceKind, deviceModelBadge, deviceName, grouped, newlyConnected, pairProblem, timeLeft, type Device } from "./devices.ts";

const day = 86_400_000;
const added = Date.UTC(2026, 10, 3, 12);
const device = (more: Partial<Device> = {}): Device => ({ id: "d1", firstSeen: added, lastSeen: added, ...more });

test("a device is called by its own name, then its model, then what it is", () => {
  assert.equal(deviceName(device({ name: "Ada's iPhone", model: "iPhone 17 Pro" })), "Ada's iPhone");
  assert.equal(deviceName(device({ model: "iPad" })), "iPad");
  assert.equal(deviceName(device({ name: "", model: "Pixel 9" })), "Pixel 9");
  assert.equal(deviceName(device({ platform: "android" })), "Android phone");
  assert.equal(deviceName(device({ platform: "ios" })), "Phone");
});

test("the model shows beside the name only when the name doesn't say it", () => {
  assert.equal(deviceModelBadge(device({ name: "Ada's iPhone", model: "iPhone 17 Pro" })), "iPhone 17 Pro");
  assert.equal(deviceModelBadge(device({ name: "iPad", model: "iPad" })), undefined);
  assert.equal(deviceModelBadge(device({ model: "iPad" })), undefined);
});

test("tablets, iPhones and other phones each get their picture", () => {
  assert.equal(deviceKind(device({ platform: "ios", model: "iPad Air" })), "ipad");
  assert.equal(deviceKind(device({ platform: "ios" })), "iphone");
  assert.equal(deviceKind(device({ model: "iPhone 16" })), "iphone");
  assert.equal(deviceKind(device({ platform: "android", model: "Pixel 9" })), "smartphone");
});

test("the detail says whether it is connected, when it last was, and when it was added", () => {
  assert.equal(deviceDetail(device({ connected: true }), added, "en-US"), "Connected · Added Nov 3");
  assert.equal(deviceDetail(device({ lastSeen: added - 3 * day }), added, "en-US"), "Last connected 3d ago · Added Nov 3");
  assert.equal(deviceDetail(device({ lastSeen: added - 20_000 }), added, "en-US"), "Last connected just now · Added Nov 3");
  assert.equal(deviceDetail(device({ lastSeen: added - 5 * 60_000 }), added, "en-US"), "Last connected 5m ago · Added Nov 3");
});

test("the device just paired is one that connected after pairing began", () => {
  const before = connectedIds([device({ id: "old", connected: true }), device({ id: "gone" })]);
  assert.deepEqual([...before], ["old"]);
  assert.equal(newlyConnected([device({ id: "old", connected: true })], before), undefined);
  assert.equal(newlyConnected([device({ id: "old", connected: true }), device({ id: "gone", connected: true })], before)?.id, "gone");
  assert.equal(newlyConnected(undefined, before), undefined);
});

test("codes read in two groups of five, and count down in minutes and seconds", () => {
  assert.equal(grouped("0123456789"), "01234 56789");
  assert.equal(grouped("1234"), "1234");
  assert.equal(timeLeft(10_000 + 42_000, 10_000), "0:42");
  assert.equal(timeLeft(10_000 + 41_200, 10_000), "0:42");
  assert.equal(timeLeft(10_000 + 300_000, 10_000), "5:00");
  assert.equal(timeLeft(5_000, 10_000), "0:00");
});

test("an am serve too old to pair says what to do instead", () => {
  assert.equal(pairProblem("unsupported request type pair_code"), "Update agentman to connect devices here, or run am pair in a terminal.");
  assert.equal(pairProblem("daemon: The relay couldn't give a code: timeout."), "The relay couldn't give a code: timeout.");
});
