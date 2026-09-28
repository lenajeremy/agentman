import assert from "node:assert/strict";
import { test } from "node:test";

import { canEnd, canResume, shouldResume } from "./resume.ts";

// Mirrors ResumeArgs in internal/source/resume.go. Offering to reopen a
// session the Mac can never reopen is worse than not offering it, because the
// failure only turns up after a round trip.
test("canResume names the agents that reopen a session by id", () => {
  for (const kind of ["claude", "codex", "cursor-cli", "kiro", "antigravity"]) {
    assert.equal(canResume(kind), true, kind);
  }
});

// OpenCode's sessions live in a running server and take prompts over its API,
// so there is no dead session to revive. Cursor's IDE chats have no CLI to
// reopen into at all.
test("canResume excludes the agents with nothing to reopen", () => {
  assert.equal(canResume("opencode"), false);
  assert.equal(canResume("cursor"), false);
  assert.equal(canResume("something-new"), false);
});

// Reopening is only for a session with no way to reach it. Doing it to one
// that already has a pane would start a second process for a conversation
// that already has one.
test("shouldResume fires only when nothing can reach the session", () => {
  assert.equal(shouldResume({ kind: "claude", inject: "none" }), true);
  assert.equal(shouldResume({ kind: "claude", inject: "tmux" }), false);
  assert.equal(shouldResume({ kind: "claude", inject: "hook" }), false);
  assert.equal(shouldResume({ kind: "opencode", inject: "api" }), false);
  // An OpenCode session with no server is still not ours to reopen.
  assert.equal(shouldResume({ kind: "opencode", inject: "none" }), false);
});

// A session someone is running in their own terminal reaches the phone
// through the hook queue. Killing that window out from under them is not what
// "end session" means.
test("canEnd covers only panes Agentman started", () => {
  assert.equal(canEnd({ kind: "claude", inject: "tmux" }), true);
  assert.equal(canEnd({ kind: "codex", inject: "tmux" }), true);
  assert.equal(canEnd({ kind: "claude", inject: "hook" }), false);
  assert.equal(canEnd({ kind: "claude", inject: "none" }), false);
  assert.equal(canEnd({ kind: "opencode", inject: "api" }), false);
});
