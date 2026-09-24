import assert from "node:assert/strict";
import test from "node:test";

import { workspaceImage } from "./imagepath.ts";

const CWD = "/Users/mac/Desktop/agentman";

test("a path inside the session is read through the workspace", () => {
  assert.deepEqual(workspaceImage("Read", `${CWD}/review-screenshots/after.png`, CWD), {
    path: "review-screenshots/after.png",
    request: "read_file",
  });
});

test("a relative path passes through", () => {
  assert.deepEqual(workspaceImage("Read", "docs/diagram.PNG", CWD), {
    path: "docs/diagram.PNG",
    request: "read_file",
  });
  assert.deepEqual(workspaceImage("Read", "./shot.jpeg", CWD), {
    path: "shot.jpeg",
    request: "read_file",
  });
});

// The whole point: agents write screenshots to temp directories, so the rule
// that excluded them excluded the files most worth looking at. The daemon
// still decides, from its own record of what the agent opened.
test("a path outside the session is read as a file the agent opened", () => {
  assert.deepEqual(workspaceImage("Read", "/tmp/shot-1234.png", CWD), {
    path: "/tmp/shot-1234.png",
    request: "read_seen_file",
  });
  assert.deepEqual(workspaceImage("Read", "/Users/mac/Downloads/IMG_1214.PNG", CWD), {
    path: "/Users/mac/Downloads/IMG_1214.PNG",
    request: "read_seen_file",
  });
});

// A sibling sharing the session's name as a prefix is outside it, so it takes
// the route that checks the transcript rather than being treated as inside.
test("a sibling with the session as a prefix is not treated as inside", () => {
  assert.deepEqual(workspaceImage("Read", `${CWD}-backup/shot.png`, CWD), {
    path: `${CWD}-backup/shot.png`,
    request: "read_seen_file",
  });
});

test("every readable format is recognised", () => {
  for (const name of ["a.png", "a.jpg", "a.jpeg", "a.gif", "a.webp", "A.PNG"]) {
    assert.deepEqual(workspaceImage("Read", name, CWD), { path: name, request: "read_file" }, name);
  }
});

test("a file that is not an image is not offered", () => {
  assert.equal(workspaceImage("Read", "main.go", CWD), null);
  assert.equal(workspaceImage("Read", "notes.png.txt", CWD), null);
});

test("a traversal is refused on either route", () => {
  assert.equal(workspaceImage("Read", "../secrets/shot.png", CWD), null);
  assert.equal(workspaceImage("Read", "docs/../../shot.png", CWD), null);
  assert.equal(workspaceImage("Read", "/tmp/../etc/shot.png", CWD), null);
});

test("a tilde is not expanded", () => {
  assert.equal(workspaceImage("Read", "~/Downloads/shot.png", CWD), null);
});

// A shell command that merely ends in an image extension is not a file.
test("a command is not a path", () => {
  assert.equal(workspaceImage("Bash", "open shot.png", CWD), null);
  assert.equal(workspaceImage("Read", "open shot.png", CWD), null);
  assert.equal(workspaceImage("Bash", "screenshot.png", CWD), null);
  assert.equal(workspaceImage("Bash", "/tmp/shot.png", CWD), null);
});

test("only the first line is considered", () => {
  assert.deepEqual(workspaceImage("Read", "shot.png\nrm -rf /", CWD), {
    path: "shot.png",
    request: "read_file",
  });
});

test("each CLI's spelling of a path tool is accepted", () => {
  for (const name of ["Read", "read", "Write", "Edit", "edit", "NotebookEdit"]) {
    assert.deepEqual(
      workspaceImage(name, "shot.png", CWD),
      { path: "shot.png", request: "read_file" },
      name,
    );
  }
});

// Without a session directory there is nothing to be inside of, so an absolute
// path takes the route the daemon authorises from the transcript.
test("an unknown session directory still allows an opened file", () => {
  assert.deepEqual(workspaceImage("Read", "/some/where/shot.png", ""), {
    path: "/some/where/shot.png",
    request: "read_seen_file",
  });
});

test("a trailing slash on the session directory is tolerated", () => {
  assert.deepEqual(workspaceImage("Read", `${CWD}/shot.png`, `${CWD}/`), {
    path: "shot.png",
    request: "read_file",
  });
});
