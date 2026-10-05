import assert from "node:assert/strict";
import test from "node:test";

import {
  artifactKind,
  artifactRowName,
  artifactTitle,
  artifactsButtonLabel,
  isMarkdown,
  sortArtifacts,
} from "./artifacts.ts";
import type { Artifact } from "./protocol.ts";

const artifact = (over: Partial<Artifact>): Artifact => ({
  name: "a.md", kind: "file", updatedAt: 0, size: 0, ...over,
});

test("every known kind has its own mark, and an unknown one reads as a file", () => {
  assert.deepEqual(artifactKind("plan"), { icon: "map", label: "Plan" });
  assert.equal(artifactKind("task").icon, "check-square");
  assert.equal(artifactKind("walkthrough").icon, "book-open");
  assert.equal(artifactKind("spec").icon, "clipboard");
  assert.equal(artifactKind("image").icon, "image");
  assert.equal(artifactKind("video").label, "Recording");
  assert.deepEqual(artifactKind("hologram"), { icon: "file-text", label: "File" });
});

test("an artifact is called by its heading, and by its name without one", () => {
  assert.equal(artifactTitle({ name: "implementation_plan.md", title: "Add dark mode" }), "Add dark mode");
  assert.equal(artifactTitle({ name: "implementation_plan.md", title: "  " }), "implementation_plan.md");
  assert.equal(artifactTitle({ name: "task.md" }), "task.md");
});

test("artifacts waiting on you come first, then the newest", () => {
  const sorted = sortArtifacts([
    artifact({ name: "old.md", updatedAt: 1 }),
    artifact({ name: "new.md", updatedAt: 9 }),
    artifact({ name: "plan.md", updatedAt: 2, review: true }),
    artifact({ name: "b.md", updatedAt: 5 }),
    artifact({ name: "a.md", updatedAt: 5 }),
  ]);
  assert.deepEqual(sorted.map((item) => item.name), ["plan.md", "new.md", "a.md", "b.md", "old.md"]);
});

test("sorting leaves the list it was given alone", () => {
  const list = [artifact({ name: "x.md", updatedAt: 1 }), artifact({ name: "y.md", updatedAt: 2 })];
  sortArtifacts(list);
  assert.deepEqual(list.map((item) => item.name), ["x.md", "y.md"]);
});

test("markdown is recognised by type or by name", () => {
  assert.ok(isMarkdown("implementation_plan.md"));
  assert.ok(isMarkdown("NOTES.MARKDOWN"));
  assert.ok(isMarkdown("plan", "text/markdown"));
  assert.ok(!isMarkdown("log.txt", "text/plain"));
  assert.ok(!isMarkdown("shot.png", "image/png"));
});

test("the artifacts button says what its badge counts", () => {
  assert.equal(artifactsButtonLabel(1, 0), "1 artifact");
  assert.equal(artifactsButtonLabel(3, 1), "3 artifacts, 1 waiting for your review");
});

test("an Artifact row opens the artifact it wrote, by name only", () => {
  assert.equal(
    artifactRowName("Artifact", "/Users/me/.gemini/antigravity-cli/brain/ef11/implementation_plan.md"),
    "implementation_plan.md",
  );
  // Any other row, even one naming a markdown file, is not an artifact.
  assert.equal(artifactRowName("Write", "/Users/me/work/README.md"), null);
  assert.equal(artifactRowName("Artifact", "implementation_plan.md"), null);
  assert.equal(artifactRowName("Artifact", "/Users/me/brain/ef11/"), null);
  assert.equal(artifactRowName("Artifact", "/Users/me/brain/ef11/.hidden"), null);
});
