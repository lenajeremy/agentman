#!/usr/bin/env node
/**
 * Add a build to site/app-version.json, the file the app reads to learn that
 * a newer build is in TestFlight. TestFlight has no API an app can ask.
 *
 *   node scripts/app-version.mjs <build> <version> <notes-file>
 *
 * The release script runs this just before its "Ship iOS build" commit and
 * commits the file with app.json, so the website announces a build in the
 * push that records it. The app then waits an hour before offering it
 * (PROCESSING_GRACE_MS in lib/updates.ts), about as long as Apple takes to
 * make an upload installable.
 */
import { readFileSync, writeFileSync } from "node:fs";

const INVITE = "https://testflight.apple.com/join/UcJvsjED";
// Enough for someone who skipped a week of builds to see what they missed.
const KEEP = 10;

const [build, version, notesFile] = process.argv.slice(2);
if (!/^\d+$/.test(build ?? "") || !version || !notesFile) {
  console.error("usage: app-version.mjs <build> <version> <notes-file>");
  process.exit(2);
}

const path = new URL("../../site/app-version.json", import.meta.url);
let published = { url: INVITE, builds: [] };
try {
  published = JSON.parse(readFileSync(path, "utf8"));
} catch {
  // A missing or broken file starts again from this build.
}

const changes = readFileSync(notesFile, "utf8")
  .split("\n")
  .map((line) => line.replace(/^•\s*/, "").trim())
  .filter(Boolean);
const entry = { build: Number(build), version, date: new Date().toISOString(), changes };
const builds = [entry, ...(published.builds ?? []).filter((b) => b.build !== entry.build)]
  .sort((a, b) => b.build - a.build)
  .slice(0, KEEP);
writeFileSync(path, JSON.stringify({ url: published.url || INVITE, builds }, null, 2) + "\n");
console.log(`site/app-version.json now offers build ${build}`);
