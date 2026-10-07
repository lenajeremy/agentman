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
  versionLabel,
} from "./updates.ts";

const release = parseAppRelease({
  url: "https://testflight.apple.com/join/UcJvsjED",
  builds: [
    { build: 26, version: "0.1.1", date: "2026-10-08T09:00:00Z", changes: ["Older news"] },
    { build: 28, version: "0.2.0", date: "2026-10-10T08:00:00Z", changes: ["Versions people can read"] },
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

test("an app behind is offered every build it is missing, newest first", () => {
  const update = appUpdate(25, release);
  assert.ok(update);
  assert.equal(update.latest.build, 28);
  assert.equal(update.latest.version, "0.2.0");
  assert.deepEqual(update.missing.map((b) => b.build), [28, 27, 26]);
});

test("an app on the latest build is not offered anything", () => {
  assert.equal(appUpdate(28, release), null);
  assert.equal(appUpdate(0, release), null);
  assert.equal(appUpdate(25, null), null);
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
  const app = appUpdate(25, release);
  const mac = macUpdate({ version: "0.14.1", latest: "0.16.0", behind: 2 });
  assert.equal(nextPrompt(app, mac, {}), "app");
  assert.equal(nextPrompt(app, mac, { app: 28 }), "mac");
  assert.equal(nextPrompt(app, mac, { app: 28, mac: "0.16.0" }), null);
  // A newer release than the one already offered is offered again.
  assert.equal(nextPrompt(app, mac, { app: 27, mac: "0.16.0" }), "app");
  assert.equal(nextPrompt(app, macUpdate({ version: "0.14.1", latest: "0.17.0", behind: 3 }), { app: 28, mac: "0.16.0" }), "mac");
});

test("Settings says where each side stands", () => {
  assert.equal(appSummary("0.1.1", 25, appUpdate(25, release)), "0.1.1 (25) · 0.2.0 is out");
  assert.equal(appSummary("0.2.0", 28, null), "0.2.0 (28) · up to date");
  assert.equal(versionLabel("0.2.0", 28), "0.2.0 (28)");
  assert.equal(versionLabel("0.2.0", 0), "0.2.0");
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
