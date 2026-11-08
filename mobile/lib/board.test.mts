import assert from "node:assert/strict";
import test from "node:test";

import {
  boardBrief,
  boardColumn,
  boardDigest,
  boardFolderLabel,
  boardSections,
  boardTextIsLong,
  checklist,
  mentionedSessions,
  paragraphs,
  plainInline,
  promptRepeatsDetail,
  stillHidden,
  worktreeParts,
} from "./board.ts";
import type { ManagerEntry, Message, Question, Session, SessionState } from "./protocol.ts";

const NOW = 1_800_000_000_000;

function session(id: string, state: SessionState, extra: Partial<Session> = {}): Session {
  return {
    id, kind: "claude", nativeId: id, name: `Agent ${id}`, cwd: "/work/api",
    state, inject: "tmux", startedAt: 0, lastActivityAt: NOW, ...extra,
  };
}

const question: Question = { id: "q", prompt: "Run it?", options: [{ key: "1", label: "Yes" }] };

test("an agent is in the column for what it wants", () => {
  const sections = boardSections([
    session("a", "busy"), session("b", "waiting_input"), session("c", "idle"), session("d", "idle", { question }),
  ], { now: NOW });
  assert.deepEqual(new Set(sections.needsYou.map((s) => s.id)), new Set(["b", "d"]));
  assert.deepEqual(sections.working.map((s) => s.id), ["a"]);
  assert.deepEqual(sections.idle.map((s) => s.id), ["c"]);
});

test("Agentman's card puts an agent in Needs you", () => {
  const sections = boardSections([session("a", "idle")], { waiting: new Set(["a"]), now: NOW });
  assert.deepEqual(sections.needsYou.map((s) => s.id), ["a"]);
  assert.equal(sections.idle.length, 0);
});

test("ended and long-idle agents are not on the Board", () => {
  const old = NOW - 5 * 24 * 60 * 60 * 1000;
  const sections = boardSections([session("a", "ended"), session("b", "idle", { lastActivityAt: old })], { now: NOW });
  assert.equal(sections.needsYou.length + sections.working.length + sections.idle.length, 0);
  assert.equal(sections.hidden.length, 0);
  // Even a long-idle agent is back the moment Agentman puts a card up about it.
  assert.equal(boardSections([session("b", "idle", { lastActivityAt: old })], { waiting: new Set(["b"]), now: NOW }).needsYou.length, 1);
});

test("hiding takes a working or idle agent off but never one that needs you", () => {
  const sections = boardSections([session("a", "busy"), session("b", "idle"), session("c", "waiting_input")], {
    hidden: new Set(["a", "b", "c"]), now: NOW,
  });
  assert.deepEqual(new Set(sections.hidden.map((s) => s.id)), new Set(["a", "b"]));
  assert.deepEqual(sections.needsYou.map((s) => s.id), ["c"]);
  assert.equal(sections.working.length + sections.idle.length, 0);
});

test("an agent swiped off the list stays off the Board until it does something", () => {
  const dismissals = { a: { activityAt: NOW, at: NOW } };
  assert.equal(boardSections([session("a", "idle")], { dismissals, now: NOW }).idle.length, 0);
  assert.equal(boardSections([session("a", "idle", { lastActivityAt: NOW + 1000 })], { dismissals, now: NOW }).idle.length, 1);
});

test("a folder narrows the Board", () => {
  const sections = boardSections([session("a", "busy"), session("b", "busy", { cwd: "/work/site" })], {
    inFolder: (cwd) => cwd.startsWith("/work/site"), now: NOW,
  });
  assert.deepEqual(sections.working.map((s) => s.id), ["b"]);
});

test("working cards keep their place as they tick", () => {
  const first = session("a", "busy", { startedAt: 200, lastActivityAt: NOW });
  const second = session("b", "busy", { startedAt: 100, lastActivityAt: NOW + 5000 });
  assert.deepEqual(boardSections([second, first], { now: NOW }).working.map((s) => s.id), ["a", "b"]);
});

test("an agent that needs you is back for good once it does", () => {
  const hidden = new Set(["a", "b"]);
  const next = stillHidden(hidden, [session("a", "waiting_input"), session("b", "busy")]);
  assert.deepEqual([...next], ["b"]);
  assert.deepEqual([...hidden], ["a", "b"], "the old set is untouched");
  // And it stays out once it goes back to work.
  assert.equal(boardSections([session("a", "busy")], { hidden: next, now: NOW }).working.length, 1);
});

const brief = (sessions: Session[]) => boardBrief(boardSections(sessions, { now: NOW }), true);

test("the brief says what the sections show", () => {
  assert.deepEqual(brief([session("a", "waiting_input"), session("b", "waiting_input"), session("c", "busy"), session("d", "busy"), session("e", "idle"), session("f", "idle")]), {
    title: "2 agents are waiting on you.",
    detail: "The rest are fine: 2 working, 2 idle.",
  });
  assert.deepEqual(brief([session("a", "waiting_input")]), { title: "1 agent is waiting on you.", detail: "" });
  assert.deepEqual(brief([session("a", "busy")]), { title: "Nothing needs you.", detail: "1 agent is working." });
  assert.deepEqual(brief([session("a", "busy"), session("b", "idle")]), { title: "Nothing needs you.", detail: "1 agent is working, 1 idle." });
  assert.deepEqual(brief([session("a", "idle")]), { title: "All quiet. Nothing needs you.", detail: "1 idle." });
  assert.deepEqual(brief([]), { title: "No agents running.", detail: "Start one and it shows up here." });
});

test("quiet is only known while connected", () => {
  assert.deepEqual(boardBrief(boardSections([], { now: NOW }), false), { title: "Waiting to reach your agents.", detail: "" });
});

test("all hidden is quiet, not empty", () => {
  const sections = boardSections([session("a", "idle")], { hidden: new Set(["a"]), now: NOW });
  assert.deepEqual(boardBrief(sections, true), { title: "All quiet. Nothing needs you.", detail: "" });
});

test("a question counts as needing the user whatever the state", () => {
  assert.equal(boardColumn({ state: "idle", question }), "needsYou");
  assert.equal(boardColumn({ state: "waiting_input" }), "needsYou");
  assert.equal(boardColumn({ state: "ended" }), "idle");
});

const tool = (id: string, name: string, title: string | undefined, extra: { summary?: string; status?: "running" | "ok" | "error"; text?: string; ts: number }): Message => ({
  id, sessionId: "a", role: "tool", ts: extra.ts, text: extra.text,
  tool: { name, summary: extra.summary, status: extra.status ?? "ok", title },
});

test("a digest reads the turn the agent is on", () => {
  const digest = boardDigest([
    tool("0", "Read", "Read the old turn", { ts: 1 }),
    { id: "1", sessionId: "a", role: "user", ts: 2, text: "Go" },
    tool("2", "Read", "Read the handler", { ts: 3 }),
    tool("3", "Edit", "Added a limiter", { ts: 4 }),
    tool("4", "TodoWrite", undefined, { text: "✔ One\n◐ Two\n○ Three", ts: 5 }),
    tool("5", "Bash", "Running the tests", { summary: "go test ./...", status: "running", ts: 6 }),
  ]);
  assert.deepEqual(digest.steps.map((step) => step.text), ["Read the handler", "Added a limiter", "Running the tests"]);
  assert.equal(digest.steps.at(-1)?.state, "running");
  assert.equal(digest.command, "go test ./...");
  assert.deepEqual(digest.progress, { done: 1, total: 3, now: "Two" });
  assert.equal(digest.turnStarted, 2);
});

test("no checklist means no bar", () => {
  assert.equal(checklist("Todos have been modified successfully."), undefined);
  assert.equal(checklist("✔ Only one"), undefined);
  assert.deepEqual(checklist("✔ A\n✕ Dropped\n○ B"), { done: 1, total: 2 });
});

test("a card finds where the question is", () => {
  const digest = boardDigest([{
    id: "1", sessionId: "a", role: "assistant", ts: 1, text: [
      "# Findings", "", "It failed in the pairing step, on the", "second try.", "",
      "```", "code", "```", "", "- Did it fail in the app or Safari?", "- Should I commit this?",
    ].join("\n"),
  }]);
  assert.equal(digest.said, "It failed in the pairing step, on the second try.");
  assert.equal(digest.asked, "Did it fail in the app or Safari? Should I commit this?");
});

test("sidechain messages and empty replies are skipped", () => {
  const digest = boardDigest([
    { id: "1", sessionId: "a", role: "assistant", ts: 1, text: "Real" },
    { id: "2", sessionId: "a", role: "assistant", ts: 2, text: "" },
    { id: "3", sessionId: "a", role: "assistant", ts: 3, text: "Side", isSidechain: true },
  ]);
  assert.equal(digest.said, "Real");
});

test("paragraphs keep what reads as prose", () => {
  assert.deepEqual(paragraphs("# Done\n\n| a | b |\n\nFirst line\nof one.\n\n> Quoted"), ["First line of one.", "Quoted"]);
});

test("a card's text is plain", () => {
  assert.equal(plainInline("It **works**, see `go test` and [the docs](http://x)."), "It works, see go test and the docs.");
  assert.equal(plainInline("snake_case_name and 2 * 3 * 4 stay"), "snake_case_name and 2 * 3 * 4 stay");
  assert.equal(plainInline("An *italic* word"), "An italic word");
});

test("a prompt that only says the command again is left out", () => {
  assert.equal(promptRepeatsDetail({ prompt: "Run git push origin main?", detail: "git push origin main" }), true);
  assert.equal(promptRepeatsDetail({ prompt: "`git status`", detail: "git status" }), true);
  assert.equal(promptRepeatsDetail({ prompt: "Should I run git push origin main before the release?", detail: "git push origin main" }), false);
  assert.equal(promptRepeatsDetail({ prompt: "Run it?", detail: undefined }), false);
  assert.equal(promptRepeatsDetail({ prompt: "Run a b?", detail: "a b\nc" }), false);
});

test("Agentman's answer names the agents it is about", () => {
  const entries: ManagerEntry[] = [
    { id: "1", role: "assistant", text: "Agent b did something earlier.", ts: 1 },
    { id: "2", role: "user", text: "What's left?", ts: 2 },
    { id: "3", role: "activity", text: "Read", ts: 3, sessionId: "a" },
    { id: "4", role: "assistant", text: "Only agent c is left.", ts: 4 },
  ];
  assert.deepEqual(mentionedSessions(entries, [session("a", "idle"), session("b", "idle"), session("c", "idle")]), new Set(["a", "c"]));
});

test("a worktree is known by its repository and its name", () => {
  assert.deepEqual(worktreeParts("/Users/me/Agentman/Worktrees/shop/fix-cart"), { repo: "shop", name: "fix-cart" });
  assert.deepEqual(worktreeParts("/Users/me/Agentman/Worktrees/shop/fix-cart/sub/dir"), { repo: "shop", name: "fix-cart" });
  assert.equal(worktreeParts("/Users/me/Agentman/Worktrees/shop"), null);
  assert.equal(worktreeParts("/Users/me/Code/shop"), null);
  assert.equal(boardFolderLabel("/Users/me/Agentman/Worktrees/shop/fix-cart"), "shop · fix-cart");
  assert.equal(boardFolderLabel("/Users/me/Code/shop"), "shop");
});

test("only text longer than its lines is cut", () => {
  assert.equal(boardTextIsLong("short", 3, 358), false);
  assert.equal(boardTextIsLong("x".repeat(300), 3, 358), true);
});
