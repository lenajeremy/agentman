/**
 * A small App Store Connect API client for the release scripts.
 *
 * No dependencies: the API's JWT is ES256, which node's crypto signs directly
 * once asked for the raw r||s form JWT uses instead of DER. The key is the
 * .p8 in ~/.appstoreconnect/private_keys, named by ASC_SIGNING_KEY_ID (Admin,
 * preferred, since some of what these scripts do needs more than uploading
 * does) or ASC_KEY_ID, with ASC_ISSUER_ID beside it. release-ios.sh loads
 * them from mobile/.asc.env.
 */
import { createPrivateKey, sign } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

// Run on their own (npm run announce:ios), the scripts read mobile/.asc.env
// themselves, as release-ios.sh does. Anything already set wins.
const envFile = new URL("../.asc.env", import.meta.url);
if (existsSync(envFile)) {
  for (const line of readFileSync(envFile, "utf8").split("\n")) {
    const match = /^\s*(?:export\s+)?([A-Z0-9_]+)=(.*)$/.exec(line);
    if (match && process.env[match[1]] === undefined) {
      process.env[match[1]] = match[2].trim().replace(/^(["'])(.*)\1$/, "$2");
    }
  }
}

const keyId = process.env.ASC_SIGNING_KEY_ID || process.env.ASC_KEY_ID;
const issuer = process.env.ASC_ISSUER_ID;

export const appId =
  process.env.ASC_APP_ID ||
  JSON.parse(readFileSync(new URL("../eas.json", import.meta.url), "utf8")).submit?.production?.ios
    ?.ascAppId;

export function requireCredentials() {
  if (!keyId || !issuer) {
    console.error("set ASC_KEY_ID (or ASC_SIGNING_KEY_ID) and ASC_ISSUER_ID, e.g. in mobile/.asc.env");
    process.exit(2);
  }
  if (!appId) {
    console.error("no App Store Connect app id: set ASC_APP_ID");
    process.exit(2);
  }
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

export class ApiError extends Error {
  constructor(method, path, status, text) {
    super(`${method} ${path}: ${status} ${text}`);
    this.status = status;
  }
}

export async function api(method, path, payload) {
  const response = await fetch(`https://api.appstoreconnect.apple.com${path}`, {
    method,
    headers: {
      Authorization: `Bearer ${token()}`,
      "Content-Type": "application/json",
    },
    body: payload ? JSON.stringify(payload) : undefined,
  });
  if (!response.ok) {
    throw new ApiError(method, path, response.status, await response.text());
  }
  return response.status === 204 ? null : response.json();
}

export const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/**
 * One build by its number, with its beta details and version. Polls for up
 * to `waitMinutes`, since a build appears a minute or two after upload.
 */
export async function findBuild(number, waitMinutes = 0) {
  const deadline = Date.now() + waitMinutes * 60_000;
  for (;;) {
    const found = await api(
      "GET",
      `/v1/builds?filter[app]=${appId}&filter[version]=${number}` +
        "&include=buildBetaDetail,preReleaseVersion&limit=1",
    );
    const build = found.data[0];
    if (build) {
      const detail = found.included?.find((item) => item.type === "buildBetaDetails");
      const release = found.included?.find((item) => item.type === "preReleaseVersions");
      return {
        id: build.id,
        number: Number(number),
        version: release?.attributes.version,
        processing: build.attributes.processingState,
        external: detail?.attributes.externalBuildState,
      };
    }
    if (Date.now() >= deadline) return null;
    await sleep(15_000);
  }
}
