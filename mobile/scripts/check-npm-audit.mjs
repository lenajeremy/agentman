import { spawnSync } from "node:child_process";

// Every exception here is a build-time dependency of Expo's own CLI with no
// patched release, and none of it reaches the app bundle. Keep the list narrow:
// any other advisory, including a new one against these same packages, must
// still fail CI. Remove an entry as soon as Expo ships the fix.
const allowedAdvisories = new Set([
  // braces <=3.0.3: stack exhaustion on deeply nested brace patterns. Reached
  // only through @expo/cli -> @expo/metro-file-map -> micromatch, which expands
  // the project's own file globs at build time. 3.0.3 is the latest release.
  "https://github.com/advisories/GHSA-vfj7-8cjw-p6xm",
  // node-forge <=1.4.0: RSA PKCS#1 v1.5 signatures accept extra nested
  // DigestAlgorithm elements. Reached only through @expo/cli and
  // @expo/code-signing-certificates, for expo-updates code signing, which this
  // app does not use. 1.4.0 is the latest release.
  "https://github.com/advisories/GHSA-86w9-cpqp-85rv",
]);

const audit = spawnSync("npm", ["audit", "--omit=dev", "--json"], {
  encoding: "utf8",
  maxBuffer: 16 * 1024 * 1024,
});
if (audit.error) throw audit.error;

let report;
try {
  report = JSON.parse(audit.stdout);
} catch {
  process.stderr.write(audit.stderr || audit.stdout || "npm audit returned no JSON\n");
  process.exit(1);
}

const vulnerabilities = report.vulnerabilities ?? {};
const memo = new Map();
function isAllowed(name, visiting = new Set()) {
  if (memo.has(name)) return memo.get(name);
  if (visiting.has(name)) return true;
  const vulnerability = vulnerabilities[name];
  if (!vulnerability || !Array.isArray(vulnerability.via)) return false;
  const next = new Set(visiting).add(name);
  const allowed = vulnerability.via.length > 0 && vulnerability.via.every((via) =>
    typeof via === "string"
      ? isAllowed(via, next)
      : typeof via?.url === "string" && allowedAdvisories.has(via.url),
  );
  memo.set(name, allowed);
  return allowed;
}

const blocked = Object.keys(vulnerabilities).filter((name) => !isAllowed(name));
if (blocked.length > 0) {
  console.error(`npm audit found unapproved vulnerabilities: ${blocked.join(", ")}`);
  process.exit(1);
}
if (Object.keys(vulnerabilities).length > 0) {
  console.log("npm audit: only the documented Expo build-tool advisories remain");
} else {
  console.log("npm audit: no vulnerabilities found");
}
