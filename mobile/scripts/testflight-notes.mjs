#!/usr/bin/env node
/**
 * Set a TestFlight build's "What to Test" notes.
 *
 * That field is what the TestFlight app shows under each build, which makes it
 * the one place a tester already looks to learn what changed — so the release
 * script fills it rather than leaving the build number as the only clue.
 *
 *   node scripts/testflight-notes.mjs <build-number> <notes-file>
 *
 * A build appears in App Store Connect a minute or two after upload, so this
 * waits for it rather than failing on the first miss. Notes can be set while
 * Apple is still processing the build.
 *
 * TestFlight shows the notes in the tester's language, and a build can carry
 * more than one: App Store Connect added an empty en-GB beside en-US, and a
 * tester reading British English saw no notes at all. So every language the
 * build has gets the same notes.
 */
import { readFileSync } from "node:fs";

import { api, findBuild, requireCredentials } from "./asc.mjs";

const [build, notesFile] = process.argv.slice(2);
if (!build || !notesFile) {
  console.error("usage: testflight-notes.mjs <build-number> <notes-file>");
  process.exit(2);
}
requireCredentials();

// Apple caps the field at 4,000 characters. Cut on a line so a bullet is
// never left half-written.
const LIMIT = 4000;
let notes = readFileSync(notesFile, "utf8").trim();
if (notes.length > LIMIT) {
  const kept = notes.slice(0, LIMIT - 40).split("\n").slice(0, -1);
  const dropped = notes.split("\n").length - kept.length;
  notes = `${kept.join("\n")}\n…and ${dropped} more`;
}

const found = await findBuild(build, 15);
if (!found) {
  console.error(`build ${build} did not appear in App Store Connect within 15 minutes`);
  process.exit(1);
}

const existing = await api("GET", `/v1/builds/${found.id}/betaBuildLocalizations`);
for (const localization of existing.data) {
  await api("PATCH", `/v1/betaBuildLocalizations/${localization.id}`, {
    data: { type: "betaBuildLocalizations", id: localization.id, attributes: { whatsNew: notes } },
  });
}
if (!existing.data.some((item) => item.attributes.locale === "en-US")) {
  await api("POST", "/v1/betaBuildLocalizations", {
    data: {
      type: "betaBuildLocalizations",
      attributes: { locale: "en-US", whatsNew: notes },
      relationships: { build: { data: { type: "builds", id: found.id } } },
    },
  });
}
console.log(`notes set on build ${build}`);
