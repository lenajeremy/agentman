import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import {
  appSummary,
  appUpdate,
  macSummary,
  macUpdate,
  nextPrompt,
  parseAppRelease,
  PROCESSING_GRACE_MS,
} from "./updates.ts";

const HOUR = 60 * 60 * 1000;
const now = Date.parse("2026-10-09T12:00:00Z");

const release = parseAppRelease({
  url: "https://testflight.apple.com/join/UcJvsjED",
  builds: [
    { build: 26, version: "0.1.1", date: "2026-10-08T09:00:00Z", changes: ["Older news"] },
    { build: 28, version: "0.1.2", date: new Date(now - 10 * 60 * 1000).toISOString(), changes: ["Still processing"] },
    { build: 27, version: "0.1.2", date: "2026-10-09T08:00:00Z", changes: ["Updates are offered"] },
    { build: "nope", version: "0.1.0", date: "2026-10-01T00:00:00Z" },
  ],
});

test("the website's file is read newest first, skipping entries it cannot use", () => {
  assert.ok(release);
  assert.deepEqual(release.builds.map((b) => b.build), [28, 27, 26]);
  assert.equal(parseAppRelease({ url: "http://insecure.example", builds: [] }), null);
  assert.equal(parseAppRelease("not json"), null);
});

test("an app a build behind is offered what it is missing", () => {
  const update = appUpdate(25, release, now);
  assert.ok(update);
  assert.equal(update.latest.build, 27);
  assert.deepEqual(update.missing.map((b) => b.build), [27, 26]);
});

// Apple takes a while to process an upload, and until it has, TestFlight has
// nothing new to install.
test("a build still processing is not offered yet", () => {
  assert.equal(appUpdate(27, release, now), null);
  const later = appUpdate(27, release, now + PROCESSING_GRACE_MS);
  assert.equal(later?.latest.build, 28);
});

test("an app on the latest build is not offered anything", () => {
  assert.equal(appUpdate(28, release, now + 24 * HOUR), null);
  assert.equal(appUpdate(0, release, now), null);
  assert.equal(appUpdate(25, null, now), null);
});

test("a Mac that is behind says so, with how to upgrade", () => {
  const update = macUpdate({
    version: "0.14.1", latest: "0.16.0", behind: 2,
    releases: [{ version: "0.16.0", changes: ["Update prompts"] }, { version: "0.15.0" }],
    upgrade: "brew upgrade --cask lenajeremy/agentman/agentman",
  });
  assert.ok(update);
  assert.equal(update.behind, 2);
  assert.equal(update.upgrade, "brew upgrade --cask lenajeremy/agentman/agentman");
  assert.match(update.changelog, /^https:\/\/github\.com\//);
});

test("a Mac that is current, unsure, or too old to say is not prompted", () => {
  assert.equal(macUpdate({ version: "0.16.0", latest: "0.16.0" }), null);
  assert.equal(macUpdate({ version: "0.16.0" }), null);
  assert.equal(macUpdate(undefined), null);
});

test("each new version is offered once, the app first", () => {
  const app = appUpdate(25, release, now);
  const mac = macUpdate({ version: "0.14.1", latest: "0.16.0", behind: 2 });
  assert.equal(nextPrompt(app, mac, {}), "app");
  assert.equal(nextPrompt(app, mac, { app: 27 }), "mac");
  assert.equal(nextPrompt(app, mac, { app: 27, mac: "0.16.0" }), null);
  // A newer release than the one already offered is offered again.
  assert.equal(nextPrompt(app, mac, { app: 26, mac: "0.16.0" }), "app");
  assert.equal(nextPrompt(app, macUpdate({ version: "0.14.1", latest: "0.17.0", behind: 3 }), { app: 27, mac: "0.16.0" }), "mac");
});

test("Settings says where each side stands", () => {
  assert.equal(appSummary("0.1.1", 25, appUpdate(25, release, now)), "0.1.1 (25) · build 27 is out");
  assert.equal(appSummary("0.1.2", 28, null), "0.1.2 (28) · up to date");
  assert.equal(macSummary({ version: "0.14.1", latest: "0.16.0", behind: 2 }), "0.14.1 · 2 versions behind");
  assert.equal(macSummary({ version: "0.14.1", latest: "0.15.0", behind: 1 }), "0.14.1 · 1 version behind");
  assert.equal(macSummary({ version: "0.16.0", latest: "0.16.0" }), "0.16.0 · up to date");
  assert.equal(macSummary({ version: "0.16.0" }), "0.16.0");
  assert.equal(macSummary(null), "Unknown");
});

// The release script writes this file and the app reads it; they must agree.
test("the website's file is one the app can read", () => {
  const published = JSON.parse(readFileSync(new URL("../../site/app-version.json", import.meta.url), "utf8"));
  const parsed = parseAppRelease(published);
  assert.ok(parsed, "site/app-version.json is not readable by the app");
  assert.ok(parsed.builds.every((build) => build.changes.length > 0), "a build has no notes");
});
