import assert from "node:assert/strict";
import test from "node:test";

import { workspaceImage } from "./imagepath.ts";

const CWD = "/Users/mac/Desktop/agentman";

test("an absolute path inside the session becomes a relative one", () => {
  assert.equal(
    workspaceImage("Read", `${CWD}/review-screenshots/after-files.png`, CWD),
    "review-screenshots/after-files.png",
  );
});

test("a relative path passes through", () => {
  assert.equal(workspaceImage("Read", "docs/diagram.PNG", CWD), "docs/diagram.PNG");
  assert.equal(workspaceImage("Read", "./shot.jpeg", CWD), "shot.jpeg");
});

test("every readable format is recognised", () => {
  for (const name of ["a.png", "a.jpg", "a.jpeg", "a.gif", "a.webp", "A.PNG"]) {
    assert.equal(workspaceImage("Read", name, CWD), name, name);
  }
});

test("a file that is not an image is not offered", () => {
  assert.equal(workspaceImage("Read", "main.go", CWD), "");
  assert.equal(workspaceImage("Read", "notes.png.txt", CWD), "");
});

test("a path outside the session is refused", () => {
  assert.equal(workspaceImage("Read", "/tmp/shot.png", CWD), "");
  assert.equal(workspaceImage("Read", "/Users/mac/Downloads/IMG_1214.PNG", CWD), "");
});

// A sibling directory sharing the session's name as a prefix is still outside
// it: /repo-backup must not read as /repo plus "-backup".
test("a sibling with the session as a prefix is refused", () => {
  assert.equal(workspaceImage("Read", `${CWD}-backup/shot.png`, CWD), "");
});

test("a traversal is refused", () => {
  assert.equal(workspaceImage("Read", "../secrets/shot.png", CWD), "");
  assert.equal(workspaceImage("Read", "docs/../../shot.png", CWD), "");
});

test("a tilde is not expanded", () => {
  assert.equal(workspaceImage("Read", "~/Downloads/shot.png", CWD), "");
});

// A shell command that merely ends in an image extension is not a file.
test("a command is not a path", () => {
  assert.equal(workspaceImage("Bash", "open shot.png", CWD), "");
  assert.equal(workspaceImage("Read", "open shot.png", CWD), "");
  assert.equal(workspaceImage("Bash", "screenshot.png", CWD), "");
});

test("only the first line is considered", () => {
  assert.equal(workspaceImage("Read", "shot.png\nrm -rf /", CWD), "shot.png");
});

test("each CLI's spelling of a path tool is accepted", () => {
  for (const name of ["Read", "read", "Write", "Edit", "edit", "NotebookEdit"]) {
    assert.equal(workspaceImage(name, "shot.png", CWD), "shot.png", name);
  }
});

test("an unknown session directory refuses absolute paths", () => {
  assert.equal(workspaceImage("Read", "/some/where/shot.png", ""), "");
});

test("a trailing slash on the session directory is tolerated", () => {
  assert.equal(workspaceImage("Read", `${CWD}/shot.png`, `${CWD}/`), "shot.png");
});
