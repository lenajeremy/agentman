// node --test web/src/thread/promptHistory.test.mts (Node strips the types).
// The Mac's PromptHistoryTests, case for case.
import { test } from "node:test";
import assert from "node:assert/strict";
import { PromptHistory, isPrompt, promptWords, promptsOf, type Prompts } from "./promptHistory.ts";

const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));

/** A history over a thread holding loaded, newest first, with older pages
 *  the daemon reads from "page 1" on. */
function history(loaded: string[], options: { cursor?: string; older?: Record<string, Prompts>; fetches?: () => void } = {}) {
  // A cursor given as undefined is none: the thread holds all there is.
  const cursor = "cursor" in options ? options.cursor : "page 1";
  const { older = {}, fetches = () => {} } = options;
  const h = new PromptHistory();
  h.sessionId = "claude:s1";
  h.loaded = () => ({ prompts: loaded, cursor, hasMore: cursor !== undefined });
  h.fetch = async (_id, at) => {
    fetches();
    return older[at] ?? null;
  };
  return h;
}

/** What ↑ shows, waiting for the daemon when it is asked. */
async function up(h: PromptHistory, text: string): Promise<string | null> {
  let shown: string | null = null;
  const took = h.older(text, (t) => (shown = t));
  for (let i = 0; i < 50 && took && shown === null; i++) await sleep(10);
  if (shown !== null) h.showing(shown);
  return shown;
}

function down(h: PromptHistory, text: string): string | null {
  let shown: string | null = null;
  h.newer(text, (t) => (shown = t));
  if (shown !== null && h.isBrowsing) h.showing(shown);
  return shown;
}

test("↑ goes back through the thread, then the daemon; ↓ comes forward to the draft", async () => {
  let fetched = 0;
  const thread = [8, 7, 6, 5, 4, 3, 2, 1].map((n) => `prompt ${n}`);
  const h = history(thread, {
    older: {
      "page 1": { prompts: ["older two", "older one"], cursor: "page 2", hasMore: true },
      "page 2": { prompts: ["the very first"], hasMore: false },
    },
    fetches: () => (fetched += 1),
  });
  let text = "half a thought";
  const seen: string[] = [];
  for (let next = await up(h, text); next !== null; next = await up(h, text)) {
    seen.push(next);
    text = next;
    if (seen.length === 2) assert.equal(fetched, 0, "the thread's own messages need no daemon, far from their end");
  }
  assert.deepEqual(seen, [...thread, "older two", "older one", "the very first"]);
  assert.equal(fetched, 2, "a page at a time, each once");
  assert.equal(h.older("the very first", () => assert.fail("nothing further back")), false);

  const back: string[] = [];
  for (let next = down(h, text); next !== null; next = down(h, text)) {
    back.push(next);
    text = next;
  }
  assert.deepEqual(back, [...seen.slice().reverse().slice(1), "half a thought"]);
  assert.equal(h.isBrowsing, false);
  assert.deepEqual(h.prompts, [], "back on the draft, nothing is held");
  assert.equal(down(h, "half a thought"), null, "↓ on the draft is the caret's");
});

test("the next page is read before it is wanted", async () => {
  let fetched = 0;
  const h = history(["three", "two", "one"], { older: { "page 1": { prompts: ["older"], hasMore: false } }, fetches: () => (fetched += 1) });
  let shown: string | null = null;
  assert.equal(h.older("", (t) => (shown = t)), true);
  assert.equal(shown, "three");
  await sleep(50);
  assert.equal(fetched, 1, "read ahead");
  assert.deepEqual(h.prompts, ["three", "two", "one", "older"]);
  h.showing("three");
  for (const want of ["two", "one", "older"]) {
    let now: string | null = null;
    assert.equal(h.older(shown ?? "", (t) => (now = t)), true);
    assert.equal(now, want, "shown at once");
    shown = now;
    h.showing(want);
  }
});

test("changed words start again", async () => {
  const h = history(["third", "second"], { cursor: undefined });
  await up(h, "");
  assert.equal(await up(h, "third"), "second");
  assert.equal(await up(h, "second, changed"), "third", "started again, with the changed words as the draft");
  assert.equal(down(h, "third"), "second, changed");
  await up(h, "");
  assert.equal(down(h, "third, changed"), null);
  assert.equal(h.isBrowsing, false);
});

test("a prompt said twice in a row comes once", async () => {
  const h = history(["again", "again", "once", "again"], { cursor: undefined });
  const seen: string[] = [];
  let text = "";
  for (let next = await up(h, text); next !== null; next = await up(h, text)) {
    seen.push(next);
    text = next;
  }
  assert.deepEqual(seen, ["again", "once", "again"]);
});

test("without the daemon, ↑ stops at the thread", async () => {
  const h = history(["only"]);
  await up(h, "");
  assert.equal(await up(h, "only"), null);
  assert.equal(h.older("only", () => assert.fail()), false);
  assert.equal(down(h, "only"), "");
});

test("no agent, or nothing said, leaves the keys alone", () => {
  const empty = history([], { cursor: undefined });
  assert.equal(empty.older("x", () => assert.fail()), false);
  assert.equal(empty.isBrowsing, false);
  assert.equal(new PromptHistory().older("x", () => assert.fail()), false);
});

test("another agent starts again", async () => {
  const h = history(["mine"], { cursor: undefined });
  await up(h, "");
  assert.equal(h.isBrowsing, true);
  h.sessionId = "codex:s2";
  assert.equal(h.isBrowsing, false);
  assert.deepEqual(h.prompts, []);
});

test("what counts as said", () => {
  const message = (role: "user" | "assistant", text?: string, { tool = false, sidechain = false } = {}) =>
    ({ id: "m", sessionId: "s", role, ts: 0, text, ...(tool ? { tool: { name: "Bash" } } : {}), ...(sidechain ? { isSidechain: true } : {}) }) as never;
  assert.equal(isPrompt(message("user", "fix the tests")), true);
  assert.equal(isPrompt(message("assistant", "done")), false);
  assert.equal(isPrompt(message("user", "a result", { tool: true })), false);
  assert.equal(isPrompt(message("user", "a subagent's", { sidechain: true })), false);
  assert.equal(isPrompt(message("user", " \n")), false);
  assert.equal(isPrompt(message("user")), false);
  assert.equal(isPrompt(message("user", "[Request interrupted by user]")), false);
  assert.equal(isPrompt(message("user", "[Request interrupted by user for tool use]")), false);
  const summary = "This session is being continued from a previous conversation that ran out of context.";
  assert.equal(isPrompt(message("user", summary)), false);
  assert.equal(isPrompt(message("user", "<artifact-content-authored-by-others/>\nThe summarized conversation included Artifact content.\n" + summary)), false);
  assert.equal(isPrompt(message("user", "<div> in the header is off by a pixel")), true);
  assert.equal(isPrompt(message("user", `why did it say "${summary}"?`)), true);
});

test("only the prompt's words come back", () => {
  const kept = "/Users/me/.agentman/images/claude-s1/a1.png";
  assert.equal(promptWords(`${kept} ${kept} fix this`), "fix this", "the daemon's paths ahead of the words");
  assert.equal(promptWords("[Image #21] fix this\n[image]"), "[Image #21] fix this", "Claude's own label stays");
  assert.equal(promptWords("look\n[image]\nand this\n[image]"), "look\nand this");
  assert.equal(promptWords(kept), null);
  assert.equal(promptWords("[image]\n"), null);
  assert.equal(promptWords("@/Users/me/shop/main.go tidy it"), "@/Users/me/shop/main.go tidy it", "files stay, as chips");
  assert.equal(promptWords("compare /tmp/a.png with the design"), "compare /tmp/a.png with the design", "a picture of the user's own, named in the words, is words");
});

test("the thread's prompts and the queue's, newest first", () => {
  const said = (id: string, text: string) => ({ id, sessionId: "s", role: "user", ts: 0, text }) as never;
  assert.deepEqual(promptsOf([said("a", "first"), said("b", "second")], ["1 image · waiting", ""]), ["1 image · waiting", "second", "first"],
    "a queued message by its words, whatever they are; pictures alone aren't");
});

test("typing while a page is read keeps the words", async () => {
  const h = history(["only"], { older: { "page 1": { prompts: ["older"], hasMore: false } } });
  const slow = h.fetch;
  h.fetch = async (id, cursor) => {
    await sleep(150);
    return slow(id, cursor);
  };
  const shown: string[] = [];
  assert.equal(h.older("draft", (t) => shown.push(t)), true);
  h.showing("only");
  assert.equal(h.older("only", (t) => shown.push(t)), true, "waits on the page");
  h.edited();
  await sleep(400);
  assert.deepEqual(shown, ["only"], "the page came, and the typed words stayed");
  assert.deepEqual(h.prompts, ["only", "older"], "though what it read is kept for the next ↑");
});
