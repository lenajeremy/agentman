import assert from "node:assert/strict";
import test from "node:test";

import { fileIcons } from "./fileIcons.ts";
import { fileIconName } from "./filetype.ts";

test("common source files get their language", () => {
  assert.equal(fileIconName("main.go"), "go");
  assert.equal(fileIconName("store.tsx"), "react");
  assert.equal(fileIconName("client.ts"), "typescript");
  assert.equal(fileIconName("script.py"), "python");
  assert.equal(fileIconName("README.md"), "markdown");
});

// Longest extension wins, or "app.d.ts" would be decided by ".ts" alone.
test("a compound extension beats its tail", () => {
  assert.equal(fileIconName("types.d.ts"), "typescript");
  assert.equal(fileIconName("diff.test.mts"), "typescript");
  assert.equal(fileIconName("docker-compose.yml"), "docker");
});

test("files that identify themselves by name", () => {
  assert.equal(fileIconName("Dockerfile"), "docker");
  assert.equal(fileIconName("go.mod"), "go");
  assert.equal(fileIconName("package-lock.json"), "lock");
  assert.equal(fileIconName("Makefile"), "settings");
  assert.equal(fileIconName("mobile/package.json"), "json");
  assert.equal(fileIconName("build/Dockerfile"), "docker");
  assert.equal(fileIconName("build/Dockerfile.dev"), "docker");
});

// package.json is JSON; package-lock.json is a lockfile. The name decides,
// not the extension, and the more specific name has to win.
test("a name rule beats the extension under it", () => {
  assert.equal(fileIconName("package.json"), "json");
  assert.equal(fileIconName("yarn.lock"), "lock");
  assert.equal(fileIconName("config.json"), "json");
});

test("case does not matter", () => {
  assert.equal(fileIconName("Main.GO"), "go");
  assert.equal(fileIconName("DOCKERFILE"), "docker");
});

test("a dotfile is a name, not an extension", () => {
  assert.equal(fileIconName(".gitignore"), "git");
  assert.equal(fileIconName(".prettierrc"), "settings");
  assert.equal(fileIconName(".env"), "settings");
  assert.equal(fileIconName(".env.local"), "settings");
});

test("directories get a folder whatever they are called", () => {
  assert.equal(fileIconName("internal", true), "folder-base");
  assert.equal(fileIconName("main.go", true), "folder-base");
});

test("an unknown type gets a document rather than nothing", () => {
  assert.equal(fileIconName("notes.xyz"), "document");
  assert.equal(fileIconName("noextension"), "document");
  assert.equal(fileIconName(""), "document");
  assert.equal(fileIconName("constructor"), "document");
  assert.equal(fileIconName("notes.constructor"), "document");
});

test("every selected icon has drawable paths with concrete colours", () => {
  for (const [name, icon] of Object.entries(fileIcons)) {
    assert.match(icon.viewBox, /^-?\d+(?:\.\d+)? -?\d+(?:\.\d+)? \d+(?:\.\d+)? \d+(?:\.\d+)?$/, name);
    assert.ok(icon.paths.length > 0, name);
    for (const path of icon.paths) {
      assert.ok(path.d.length > 0, name);
      assert.match(path.fill, /^#[\da-fA-F]{6}$/, name);
    }
  }

  for (const filename of ["README.md", "store.tsx", "Dockerfile", "photo.png", "notes.xyz"]) {
    assert.ok(fileIcons[fileIconName(filename)], filename);
  }
});
