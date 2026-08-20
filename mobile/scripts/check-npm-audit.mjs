import { spawnSync } from "node:child_process";

// Expo SDK 54's Metro pins image-size 1.x. No patched image-size release
// exists for these two parser-loop advisories yet. Agentman's Metro config
// removes the only affected format in Expo's asset list (.heic), while ICNS
// and JXL are not accepted assets. Keep this exception narrow: any new npm
// advisory, including another image-size advisory, must fail CI.
const allowedAdvisories = new Set([
  "https://github.com/advisories/GHSA-w3rx-r6r6-pgpr",
  "https://github.com/advisories/GHSA-5p2g-fcmc-qvqq",
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
  console.log("npm audit: only the documented Expo 54 image-size advisories remain");
} else {
  console.log("npm audit: no vulnerabilities found");
}
