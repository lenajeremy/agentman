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
 * No dependencies: the App Store Connect JWT is ES256, which node's crypto
 * signs directly once asked for the raw r||s form JWT uses instead of DER.
 */
import { createPrivateKey, sign } from "node:crypto";
import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

const [build, notesFile] = process.argv.slice(2);
if (!build || !notesFile) {
  console.error("usage: testflight-notes.mjs <build-number> <notes-file>");
  process.exit(2);
}

// Apple caps the field at 4,000 characters. Cut on a line so a bullet is
// never left half-written.
const LIMIT = 4000;
let notes = readFileSync(notesFile, "utf8").trim();
if (notes.length > LIMIT) {
  const kept = notes.slice(0, LIMIT - 40).split("\n").slice(0, -1);
  const dropped = notes.split("\n").length - kept.length;
  notes = `${kept.join("\n")}\n…and ${dropped} more`;
}

// Setting notes needs more than uploading does, so the signing key — which
// has Admin — is preferred when there is one.
const keyId = process.env.ASC_SIGNING_KEY_ID || process.env.ASC_KEY_ID;
const issuer = process.env.ASC_ISSUER_ID;
if (!keyId || !issuer) {
  console.error("set ASC_KEY_ID (or ASC_SIGNING_KEY_ID) and ASC_ISSUER_ID");
  process.exit(2);
}
const appId =
  process.env.ASC_APP_ID ||
  JSON.parse(readFileSync(new URL("../eas.json", import.meta.url), "utf8"))
    .submit?.production?.ios?.ascAppId;
if (!appId) {
  console.error("no App Store Connect app id: set ASC_APP_ID");
  process.exit(2);
}

const b64url = (value) => Buffer.from(value).toString("base64url");
function token() {
  const now = Math.floor(Date.now() / 1000);
  const head = b64url(JSON.stringify({ alg: "ES256", kid: keyId, typ: "JWT" }));
  const body = b64url(
    JSON.stringify({ iss: issuer, iat: now, exp: now + 600, aud: "appstoreconnect-v1" }),
  );
  const key = createPrivateKey(
    readFileSync(join(homedir(), ".appstoreconnect/private_keys", `AuthKey_${keyId}.p8`)),
  );
  const signature = sign("sha256", Buffer.from(`${head}.${body}`), {
    key,
    dsaEncoding: "ieee-p1363",
  });
  return `${head}.${body}.${b64url(signature)}`;
}

async function api(method, path, payload) {
  const response = await fetch(`https://api.appstoreconnect.apple.com${path}`, {
    method,
    headers: {
      Authorization: `Bearer ${token()}`,
      "Content-Type": "application/json",
    },
    body: payload ? JSON.stringify(payload) : undefined,
  });
  if (!response.ok) {
    throw new Error(`${method} ${path}: ${response.status} ${await response.text()}`);
  }
  return response.status === 204 ? null : response.json();
}

// Wait for the upload to become a build Apple knows about.
let buildId;
for (let attempt = 0; attempt < 60 && !buildId; attempt++) {
  const found = await api(
    "GET",
    `/v1/builds?filter[app]=${appId}&filter[version]=${build}&limit=1`,
  );
  buildId = found.data[0]?.id;
  if (!buildId) await new Promise((resolve) => setTimeout(resolve, 15_000));
}
if (!buildId) {
  console.error(`build ${build} did not appear in App Store Connect within 15 minutes`);
  process.exit(1);
}

const existing = await api("GET", `/v1/builds/${buildId}/betaBuildLocalizations`);
const localization = existing.data.find((item) => item.attributes.locale === "en-US");
if (localization) {
  await api("PATCH", `/v1/betaBuildLocalizations/${localization.id}`, {
    data: { type: "betaBuildLocalizations", id: localization.id, attributes: { whatsNew: notes } },
  });
} else {
  await api("POST", "/v1/betaBuildLocalizations", {
    data: {
      type: "betaBuildLocalizations",
      attributes: { locale: "en-US", whatsNew: notes },
      relationships: { build: { data: { type: "builds", id: buildId } } },
    },
  });
}
console.log(`notes set on build ${build}`);
