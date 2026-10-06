#!/usr/bin/env node
// Publishes agentman to npm from a release's archives.
//
//   node scripts/npm-release.mjs <version> <archive-dir> [--publish]
//
// <archive-dir> holds GoReleaser's agentman_v<version>_<os>_<arch>.tar.gz
// files (its dist/ after a release). For each of the four builds this makes
// an agentman-<os>-<cpu> package holding just the `am` binary, with "os" and
// "cpu" set so npm only ever installs the one that matches. Then it makes the
// agentman package itself, which depends on all four as optional
// dependencies at exactly this version and runs whichever one npm installed.
//
// Without --publish it stops after `npm pack`, leaving the tarballs in
// npm/out/ to install and try. With --publish the four platform packages go
// first, so the agentman package never points at versions that are not there.

import { execFileSync } from "node:child_process";
import { chmodSync, cpSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const [versionArg, archiveDir, ...flags] = process.argv.slice(2);
if (!versionArg || !archiveDir) {
  console.error("usage: node scripts/npm-release.mjs <version> <archive-dir> [--publish]");
  process.exit(2);
}
const version = versionArg.replace(/^v/, "");
if (!/^\d+\.\d+\.\d+(-[\w.]+)?$/.test(version)) throw new Error(`not a version: ${versionArg}`);
const publish = flags.includes("--publish");

// GoReleaser's names on the left, Node's process.platform and process.arch on the right.
const TARGETS = [
  { os: "darwin", arch: "arm64", npmOs: "darwin", cpu: "arm64" },
  { os: "darwin", arch: "amd64", npmOs: "darwin", cpu: "x64" },
  { os: "linux", arch: "arm64", npmOs: "linux", cpu: "arm64" },
  { os: "linux", arch: "amd64", npmOs: "linux", cpu: "x64" },
];

const template = JSON.parse(readFileSync(join(root, "npm/agentman/package.json"), "utf8"));
const out = join(root, "npm/out");
rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });

const run = (cmd, args, cwd) => execFileSync(cmd, args, { cwd, stdio: ["ignore", "pipe", "inherit"] }).toString().trim();

const packages = [];
for (const target of TARGETS) {
  const name = `agentman-${target.npmOs}-${target.cpu}`;
  const archive = resolve(archiveDir, `agentman_v${version}_${target.os}_${target.arch}.tar.gz`);
  if (!existsSync(archive)) throw new Error(`missing ${archive}`);
  const dir = join(out, name);
  mkdirSync(join(dir, "bin"), { recursive: true });
  run("tar", ["-xzf", archive, "-C", join(dir, "bin"), "am"]);
  chmodSync(join(dir, "bin/am"), 0o755);
  writeFileSync(join(dir, "package.json"), JSON.stringify({
    name,
    version,
    description: `The am binary of agentman for ${target.npmOs} on ${target.cpu}. Install agentman instead.`,
    homepage: template.homepage,
    repository: template.repository,
    license: template.license,
    os: [target.npmOs],
    cpu: [target.cpu],
    files: ["bin/am"],
    preferUnplugged: true,
  }, null, 2) + "\n");
  writeFileSync(join(dir, "README.md"), `# ${name}\n\nThe \`am\` binary of [agentman](https://www.npmjs.com/package/agentman) for ${target.npmOs} on ${target.cpu}.\nInstall \`agentman\` instead; npm picks this package for you.\n`);
  packages.push(dir);
}

const main = join(out, "agentman");
cpSync(join(root, "npm/agentman"), main, { recursive: true });
const manifest = { ...template, version };
manifest.optionalDependencies = Object.fromEntries(Object.keys(template.optionalDependencies).map((name) => [name, version]));
for (const dir of packages) {
  const name = JSON.parse(readFileSync(join(dir, "package.json"), "utf8")).name;
  if (!(name in manifest.optionalDependencies)) throw new Error(`npm/agentman/package.json does not depend on ${name}`);
}
writeFileSync(join(main, "package.json"), JSON.stringify(manifest, null, 2) + "\n");
packages.push(main);

for (const dir of packages) {
  const tarball = run("npm", ["pack", "--pack-destination", out, "--silent"], dir);
  console.log(`packed ${tarball}`);
  if (publish) {
    run("npm", ["publish", "--access", "public", ...(process.env.NPM_CONFIG_PROVENANCE ? ["--provenance"] : [])], dir);
    console.log(`published ${tarball.replace(/\.tgz$/, "")}`);
  }
}
