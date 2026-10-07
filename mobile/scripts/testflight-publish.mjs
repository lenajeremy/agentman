#!/usr/bin/env node
/**
 * Send a build to the testers outside the team: every external TestFlight
 * group, the public link's among them.
 *
 *   node scripts/testflight-publish.mjs <build-number>
 *
 * An upload only reaches the internal group. Builds 12 to 26 went no further,
 * and for two weeks the public link served build 11, because adding each
 * build to the group and submitting it for Beta App Review was never done.
 *
 * Apple must have finished processing the build first, so this waits for
 * that, for up to 30 minutes. The first build of a new version then goes
 * through Beta App Review, which usually takes hours; later builds of the
 * same version are usually let through at once. Either way the build is with
 * Apple when this returns, and `npm run announce:ios` tells the app once
 * testers can install it.
 */
import { ApiError, api, appId, findBuild, requireCredentials, sleep } from "./asc.mjs";

const [number] = process.argv.slice(2);
if (!/^\d+$/.test(number ?? "")) {
  console.error("usage: testflight-publish.mjs <build-number>");
  process.exit(2);
}
requireCredentials();

let build = await findBuild(number, 15);
if (!build) {
  console.error(`build ${number} did not appear in App Store Connect within 15 minutes`);
  process.exit(1);
}
const deadline = Date.now() + 30 * 60_000;
while (build.processing !== "VALID") {
  if (build.processing === "FAILED" || build.processing === "INVALID") {
    console.error(`Apple could not process build ${number} (${build.processing})`);
    process.exit(1);
  }
  if (Date.now() > deadline) {
    console.error(`build ${number} is still processing; run this again later`);
    process.exit(1);
  }
  await sleep(30_000);
  build = await findBuild(number);
}

const groups = await api("GET", `/v1/apps/${appId}/betaGroups?limit=50`);
for (const group of groups.data.filter((g) => !g.attributes.isInternalGroup)) {
  await api("POST", `/v1/betaGroups/${group.id}/relationships/builds`, {
    data: [{ type: "builds", id: build.id }],
  });
  console.log(`build ${number} added to ${group.attributes.name}`);
}

try {
  await api("POST", "/v1/betaAppReviewSubmissions", {
    data: {
      type: "betaAppReviewSubmissions",
      relationships: { build: { data: { type: "builds", id: build.id } } },
    },
  });
  console.log(`build ${number} submitted for Beta App Review`);
} catch (error) {
  // Already submitted, or a later build of an approved version that Apple
  // lets through without one.
  if (!(error instanceof ApiError) || (error.status !== 409 && error.status !== 422)) throw error;
  console.log(`build ${number} needs no new review submission`);
}
console.log(`build ${number} is ${(await findBuild(number)).external} for external testers`);
