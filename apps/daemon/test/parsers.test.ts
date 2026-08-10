import { strict as assert } from "node:assert";
import { test } from "node:test";
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { collectBackward, JsonlTail } from "../src/jsonl.ts";
import { ClaudeParser } from "../src/parsers/claude.ts";
import { CodexParser, codexStateFromLine } from "../src/parsers/codex.ts";

async function fixture(records: unknown[]): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), "agentman-parse-"));
  const path = join(dir, "t.jsonl");
  await writeFile(path, records.map((r) => JSON.stringify(r)).join("\n") + "\n");
  return path;
}

/* --------------------------------- Claude -------------------------------- */

const claudeUser = (uuid: string, text: string) => ({
  type: "user",
  uuid,
  timestamp: "2026-08-10T10:00:00.000Z",
  message: { role: "user", content: text },
});

const claudeAssistant = (uuid: string, content: unknown[]) => ({
  type: "assistant",
  uuid,
  timestamp: "2026-08-10T10:00:01.000Z",
  message: { role: "assistant", content },
});

const claudeToolResult = (uuid: string, toolUseId: string, content: unknown, isError = false) => ({
  type: "user",
  uuid,
  timestamp: "2026-08-10T10:00:02.000Z",
  message: {
    role: "user",
    content: [{ type: "tool_result", tool_use_id: toolUseId, content, is_error: isError }],
  },
});

test("Claude: a turn expands to text plus tool rows in order", async () => {
  const path = await fixture([
    claudeUser("u1", "run the tests"),
    claudeAssistant("a1", [
      { type: "text", text: "Running them now." },
      { type: "tool_use", id: "toolu_1", name: "Bash", input: { command: "npm test" } },
    ]),
    claudeToolResult("u2", "toolu_1", "7 passing"),
  ]);

  const parser = new ClaudeParser("claude:s");
  const page = await collectBackward(path, { want: 20, map: (t, o) => parser.parse(t, o) });

  assert.deepEqual(
    page.items.map((m) => [m.role, m.text ?? m.tool?.name]),
    [
      ["user", "run the tests"],
      ["assistant", "Running them now."],
      ["tool", "Bash"],
    ],
    "a tool call and its result must collapse into one row",
  );
  assert.equal(page.items[2]?.tool?.summary, "npm test");
  assert.equal(page.items[2]?.tool?.status, "ok");
  assert.equal(page.items[2]?.text, "7 passing");
});

test("Claude: failed tools are marked as errors", async () => {
  const path = await fixture([
    claudeAssistant("a1", [
      { type: "tool_use", id: "toolu_9", name: "Bash", input: { command: "false" } },
    ]),
    claudeToolResult("u2", "toolu_9", "Permission to use Bash has been denied", true),
  ]);

  const parser = new ClaudeParser("claude:s");
  const page = await collectBackward(path, { want: 20, map: (t, o) => parser.parse(t, o) });

  assert.equal(page.items.length, 1);
  assert.equal(page.items[0]?.tool?.status, "error");
});

test("Claude: live tailing settles a running tool under the same id", async () => {
  // Forwards we meet the call before its result, so the row is emitted twice:
  // once running, once settled. Sharing an id is what lets the app upsert
  // rather than show the tool twice.
  const path = await fixture([
    claudeAssistant("a1", [
      { type: "tool_use", id: "toolu_5", name: "Read", input: { file_path: "/tmp/x.ts" } },
    ]),
    claudeToolResult("u2", "toolu_5", "contents"),
  ]);

  const parser = new ClaudeParser("claude:s");
  const tail = new JsonlTail(path);
  const emitted = (await tail.read()).flatMap((l) => parser.parse(l.text, l.offset));

  assert.equal(emitted.length, 2);
  assert.equal(emitted[0]?.id, emitted[1]?.id, "both rows must share the tool_use id");
  assert.equal(emitted[0]?.tool?.status, "running");
  assert.equal(emitted[1]?.tool?.status, "ok");
  assert.equal(emitted[0]?.tool?.summary, "/tmp/x.ts");
});

test("Claude: bookkeeping records and thinking blocks stay out of the feed", async () => {
  const path = await fixture([
    { type: "mode", mode: "normal", sessionId: "s" },
    { type: "permission-mode", permissionMode: "auto", sessionId: "s" },
    { type: "ai-title", title: "Something" },
    { type: "file-history-snapshot", messageId: "m" },
    {
      type: "system",
      uuid: "s1",
      content: "<command-name>/resume</command-name>",
      timestamp: "2026-08-10T10:00:00.000Z",
    },
    { ...claudeUser("u0", "hi"), isMeta: true },
    claudeAssistant("a1", [
      { type: "thinking", thinking: "long internal monologue", signature: "abc" },
      { type: "text", text: "Hello." },
    ]),
  ]);

  const parser = new ClaudeParser("claude:s");
  const page = await collectBackward(path, { want: 20, map: (t, o) => parser.parse(t, o) });

  assert.deepEqual(page.items.map((m) => m.text), ["Hello."]);
});

test("Claude: subagent output is flagged rather than inlined silently", async () => {
  const path = await fixture([
    { ...claudeAssistant("a1", [{ type: "text", text: "from a subagent" }]), isSidechain: true },
  ]);

  const parser = new ClaudeParser("claude:s");
  const page = await collectBackward(path, { want: 5, map: (t, o) => parser.parse(t, o) });

  assert.equal(page.items[0]?.isSidechain, true);
});

/* --------------------------------- Codex --------------------------------- */

const codexItem = (item: unknown, ts = "2026-08-10T10:00:00.000Z") => ({
  timestamp: ts,
  type: "event_msg",
  payload: { type: "item_completed", item },
});

test("Codex: reads the event stream and ignores the duplicate history stream", async () => {
  const path = await fixture([
    // The same assistant turn appears in both streams; only one may surface.
    {
      timestamp: "2026-08-10T10:00:00.000Z",
      type: "response_item",
      payload: { type: "message", role: "assistant", content: [{ type: "output_text", text: "hi" }] },
    },
    codexItem({ type: "AgentMessage", id: "msg_1", content: [{ type: "Text", text: "hi" }] }),
    // `developer` records are injected instructions the user never wrote.
    {
      timestamp: "2026-08-10T10:00:00.000Z",
      type: "response_item",
      payload: { type: "message", role: "developer", content: [{ type: "input_text", text: "<skills>" }] },
    },
  ]);

  const parser = new CodexParser("codex:s");
  const page = await collectBackward(path, { want: 20, map: (t, o) => parser.parse(t, o) });

  assert.deepEqual(page.items.map((m) => [m.role, m.text]), [["assistant", "hi"]]);
});

test("Codex: commands use the parsed form and report exit status", async () => {
  const path = await fixture([
    codexItem({
      type: "CommandExecution",
      id: "exec-1",
      command: ["/bin/zsh", "-lc", "go test ./..."],
      parsed_cmd: [{ type: "run", cmd: "go test ./..." }],
      status: "completed",
      exit_code: 0,
      aggregated_output: "ok  	all tests pass",
    }),
    codexItem({
      type: "CommandExecution",
      id: "exec-2",
      command: ["/bin/zsh", "-lc", "false"],
      parsed_cmd: [{ type: "run", cmd: "false" }],
      status: "completed",
      exit_code: 1,
    }),
  ]);

  const parser = new CodexParser("codex:s");
  const page = await collectBackward(path, { want: 20, map: (t, o) => parser.parse(t, o) });

  assert.equal(page.items[0]?.tool?.summary, "go test ./...", "prefer parsed_cmd over the shell wrapper");
  assert.equal(page.items[0]?.tool?.status, "ok");
  assert.equal(page.items[1]?.tool?.status, "error", "a non-zero exit is a failure");
});

test("Codex: file changes summarize to paths", async () => {
  const path = await fixture([
    codexItem({
      type: "FileChange",
      id: "fc-1",
      changes: { "/repo/tasks.md": { type: "update", unified_diff: "@@" } },
    }),
  ]);

  const parser = new CodexParser("codex:s");
  const page = await collectBackward(path, { want: 5, map: (t, o) => parser.parse(t, o) });

  assert.equal(page.items[0]?.tool?.name, "Edit");
  assert.equal(page.items[0]?.tool?.summary, "/repo/tasks.md");
});

test("Codex: turn boundaries drive the busy/idle state", () => {
  const started = JSON.stringify({ type: "event_msg", payload: { type: "task_started" } });
  const done = JSON.stringify({ type: "event_msg", payload: { type: "task_complete" } });
  const aborted = JSON.stringify({ type: "event_msg", payload: { type: "turn_aborted" } });
  const noise = JSON.stringify({ type: "event_msg", payload: { type: "token_count" } });

  assert.equal(codexStateFromLine(started), "busy");
  assert.equal(codexStateFromLine(done), "idle");
  assert.equal(codexStateFromLine(aborted), "idle");
  assert.equal(codexStateFromLine(noise), undefined);
});

test("both parsers survive malformed lines", async () => {
  const path = await fixture([]);
  await writeFile(path, '{"type":"user"\nnot json at all\n{}\n');

  const claude = new ClaudeParser("claude:s");
  const codex = new CodexParser("codex:s");
  const a = await collectBackward(path, { want: 5, map: (t, o) => claude.parse(t, o) });
  const b = await collectBackward(path, { want: 5, map: (t, o) => codex.parse(t, o) });

  assert.deepEqual(a.items, []);
  assert.deepEqual(b.items, []);
});
