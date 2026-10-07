import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { test } from "node:test";

// @ts-expect-error: a plain .mjs script, shared with the release script.
import { bump } from "../scripts/semver.mjs";

test("each part of a version moves the way semantic versioning says", () => {
  assert.equal(bump("0.1.1", "patch"), "0.1.2");
  assert.equal(bump("0.1.1", "minor"), "0.2.0");
  assert.equal(bump("0.9.4", "major"), "1.0.0");
  assert.equal(bump("1.9.9", "minor"), "1.10.0");
});

test("a release that does not say what it is, or a version that is not one, is refused", () => {
  assert.throws(() => bump("0.1.1", "big"));
  assert.throws(() => bump("0.1", "patch"));
  assert.throws(() => bump("v0.1.1", "patch"));
});

// The release script calls it as a command.
test("the release script gets the next version from it", () => {
  const next = execFileSync("node", [new URL("../scripts/semver.mjs", import.meta.url).pathname, "minor", "0.1.1"], {
    encoding: "utf8",
  });
  assert.equal(next, "0.2.0");
});
