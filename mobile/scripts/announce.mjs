#!/usr/bin/env node
/**
 * Tell the app about a build, once testers can install it.
 *
 *   npm run announce:ios                 the latest build shipped from here
 *   npm run announce:ios -- 27           a particular build
 *   npm run announce:ios -- --wait 20    keep checking for up to 20 minutes
 *
 * The app learns of a newer build from site/app-version.json, since
 * TestFlight has no API an app can ask (see lib/updates.ts). An entry there
 * makes every older app offer the build, so one is only added once the build
 * is in beta testing for testers outside the team. An Update button that
 * leads to the old build is worse than saying nothing, and the first build of
 * a new version waits on Beta App Review for that, often for hours. That is
 * why this is its own command: the release script runs it once at the end,
 * and says to run it again if Apple has not approved the build yet.
 *
 * The notes are the build's own "What to Test", read back from TestFlight.
 * Committing the file and pushing main is what publishes it.
 */
import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";

import { api, findBuild, requireCredentials, sleep } from "./asc.mjs";

const INVITE = "https://testflight.apple.com/join/UcJvsjED";
// Enough for someone who skipped a few weeks of builds to see what they missed.
const KEEP = 10;

const args = process.argv.slice(2);
const waitAt = args.indexOf("--wait");
const waitMinutes = waitAt >= 0 ? Number(args.splice(waitAt, 2)[1]) : 0;
if (!Number.isFinite(waitMinutes) || waitMinutes < 0) {
  console.error("usage: announce.mjs [<build-number>] [--wait <minutes>]");
  process.exit(2);
}
requireCredentials();

const root = new URL("../../", import.meta.url);
const git = (...gitArgs) =>
  execFileSync("git", gitArgs, { cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "inherit"] }).trim();

let number = args[0];
if (!number) {
  const tag = git("describe", "--tags", "--match", "ios-build-*", "--abbrev=0");
  number = tag.replace("ios-build-", "");
}
if (!/^\d+$/.test(number)) {
  console.error(`not a build number: ${number}`);
  process.exit(2);
}

const deadline = Date.now() + waitMinutes * 60_000;
let build = await findBuild(number);
while (build && build.external !== "IN_BETA_TESTING" && Date.now() < deadline) {
  await sleep(60_000);
  build = await findBuild(number);
}
if (!build) {
  console.error(`build ${number} is not in App Store Connect`);
  process.exit(1);
}
if (build.external !== "IN_BETA_TESTING") {
  console.log(`build ${number} is ${build.external} for external testers, so the app is not told yet.`);
  console.log("Run `npm run announce:ios` again once Apple has approved it.");
  process.exit(3);
}

const localizations = await api("GET", `/v1/builds/${build.id}/betaBuildLocalizations`);
const notes =
  localizations.data.find((item) => item.attributes.locale === "en-US")?.attributes.whatsNew ??
  localizations.data.find((item) => item.attributes.whatsNew)?.attributes.whatsNew ??
  "";
const changes = notes
  .split("\n")
  .map((line) => line.replace(/^•\s*/, "").trim())
  .filter(Boolean);

// Pushing main is what publishes the file, so this only runs there, and
// starts from what is already pushed.
if (git("rev-parse", "--abbrev-ref", "HEAD") !== "main") {
  console.error("run this on main: the website is deployed from it");
  process.exit(2);
}
git("pull", "-q", "--ff-only");

const path = new URL("site/app-version.json", root);
let published = { url: INVITE, builds: [] };
try {
  published = JSON.parse(readFileSync(path, "utf8"));
} catch {
  // A missing or broken file starts again from this build.
}
if (published.builds?.some((entry) => entry.build === build.number)) {
  console.log(`build ${number} is already announced`);
  process.exit(0);
}
const entry = { build: build.number, version: build.version, date: new Date().toISOString(), changes };
const builds = [entry, ...(published.builds ?? [])].sort((a, b) => b.build - a.build).slice(0, KEEP);
writeFileSync(path, JSON.stringify({ url: published.url || INVITE, builds }, null, 2) + "\n");

git("commit", "-q", "-m", `Announce iOS build ${number}`, "--", "site/app-version.json");
git("push", "-q", "origin", "HEAD");
console.log(`announced build ${number} (${build.version}); the website has it once Vercel deploys main`);
