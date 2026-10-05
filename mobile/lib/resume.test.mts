import assert from "node:assert/strict";
import { test } from "node:test";

import { canEnd, canResume, resumeOnce, shouldResume } from "./resume.ts";

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
  assert.equal(shouldResume({ kind: "claude", inject: "none", state: "ended" }), true);
  assert.equal(shouldResume({ kind: "claude", inject: "tmux", state: "idle" }), false);
  assert.equal(shouldResume({ kind: "claude", inject: "hook", state: "idle" }), false);
  assert.equal(shouldResume({ kind: "opencode", inject: "api", state: "idle" }), false);
  // An OpenCode session with no server is still not ours to reopen.
  assert.equal(shouldResume({ kind: "opencode", inject: "none", state: "ended" }), false);
});

// A session running in someone's own terminal is live but unreachable from
// here. Reopening it started a second agent on the same conversation.
test("shouldResume leaves a session that is still running alone", () => {
  for (const kind of ["kiro", "antigravity", "codex", "cursor-cli"]) {
    for (const state of ["idle", "busy", "waiting_input"]) {
      assert.equal(shouldResume({ kind, inject: "none", state }), false, `${kind} ${state}`);
    }
  }
});

// The screen can be opened again before the reopened agent has registered,
// and two taps can race. Either way the Mac must be asked once.
test("resumeOnce asks the Mac once while a resume is starting or just started", async () => {
  let now = 1_000;
  const asked: string[] = [];
  let finish: (id: string) => void = () => {};
  const resume = resumeOnce((sessionId) => {
    asked.push(sessionId);
    return new Promise<string>((resolve) => { finish = resolve; });
  }, () => now);

  const first = resume("kiro:abc");
  const second = resume("kiro:abc");
  finish("kiro:abc");
  assert.equal(await first, "kiro:abc");
  assert.equal(await second, "kiro:abc");
  now += 10_000;
  assert.equal(await resume("kiro:abc"), "kiro:abc");
  assert.deepEqual(asked, ["kiro:abc"]);

  // Long after, a session that ended again may be reopened again.
  now += 60_000;
  void resume("kiro:abc");
  assert.deepEqual(asked, ["kiro:abc", "kiro:abc"]);
});

test("resumeOnce lets a failed resume be tried again", async () => {
  const asked: string[] = [];
  let fail = true;
  const resume = resumeOnce(async (sessionId) => {
    asked.push(sessionId);
    if (fail) throw new Error("not logged in");
    return sessionId;
  }, () => 0);
  await assert.rejects(resume("claude:x"));
  fail = false;
  assert.equal(await resume("claude:x"), "claude:x");
  assert.equal(asked.length, 2);
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
