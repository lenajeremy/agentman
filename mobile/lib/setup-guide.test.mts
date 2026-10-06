import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { test } from "node:test";

import { INSTALL_COMMAND, SETUP_GUIDE_URL, SETUP_STEPS } from "./setup-guide.ts";

// The guide is only useful if every command in it is one `am` really has.
// These read the CLI's own usage text and the site's files, so renaming a
// command or moving the install script breaks this test, not a new user.
const usage = readFileSync(new URL("../../cmd/am/main.go", import.meta.url), "utf8");

test("every am command in the pairing guide is one the CLI has", () => {
  const commands = SETUP_STEPS.flatMap((step) => (step.command?.startsWith("am ") ? [step.command] : []));
  assert.deepEqual(commands, ["am install-hooks", "am serve", "am pair"]);
  for (const command of commands) {
    assert.match(usage, new RegExp(`^  ${command}\\b`, "m"), `am's usage does not list ${command}`);
  }
});

test("the install command and the guide point at files the site serves", () => {
  const site = new URL("../../site/", import.meta.url);
  assert.ok(existsSync(new URL("install.sh", site)), "site/install.sh is missing");
  assert.ok(existsSync(new URL("start.html", site)), "site/start.html is missing");
  assert.match(INSTALL_COMMAND, /^curl -fsSL https:\/\/[^\s]+\/install \| sh$/);
  assert.equal(new URL(SETUP_GUIDE_URL).pathname, "/start");
  const vercel = JSON.parse(readFileSync(new URL("../../vercel.json", import.meta.url), "utf8"));
  assert.ok(
    vercel.rewrites?.some((r: { source: string; destination: string }) => r.source === "/install" && r.destination === "/install.sh"),
    "vercel.json no longer serves /install",
  );
});

test("the guide ends on the code the phone scans", () => {
  assert.equal(SETUP_STEPS.at(-1)?.command, "am pair");
});
