import assert from "node:assert/strict";
import test from "node:test";

import {
  commandCall,
  commandSend,
  listCommands,
  matchCommands,
  pickedCommand,
  runPanel,
  slashQuery,
  takesCommands,
} from "./commands.ts";
import type { DaemonEvent, Request, SlashCommand } from "./protocol.ts";

const commands: SlashCommand[] = [
  { name: "compact", argumentHint: "[instructions]", source: "builtin" },
  { name: "context", source: "builtin" },
  { name: "usage", source: "builtin", panel: true },
  { name: "mcp", argumentHint: "[reconnect|enable|disable <server>]", source: "builtin", panel: true },
  { name: "model", argumentHint: "<model>", source: "builtin" },
  { name: "code-review", argumentHint: "[level] [target]", source: "bundled" },
  { name: "vercel:deploy", source: "plugin" },
  { name: "add-dir", source: "personal" },
  { name: "help", source: "builtin", panel: true },
  { name: "hello", argumentHint: "[name]", source: "project" },
];

test("the menu is open while the command's name is being typed", () => {
  assert.equal(slashQuery("/"), "");
  assert.equal(slashQuery("/comp"), "comp");
  assert.equal(slashQuery("/compact "), null);
  assert.equal(slashQuery("look at /tmp"), null);
  assert.equal(slashQuery(""), null);
});

test("matches lead with the name, then names it starts, then words within", () => {
  const names = (query: string) => matchCommands(commands, query).map((c) => c.name);
  assert.deepEqual(names("hel"), ["help", "hello"]);
  assert.deepEqual(names("deploy"), ["vercel:deploy"]);
  assert.deepEqual(names("adddir"), ["add-dir"]);
  assert.deepEqual(names("review"), ["code-review"]);
  assert.equal(names("")[0], "compact", "built-in commands lead the full list");
  assert.deepEqual(names("zzz"), []);
});

test("a picked command leaves room for its argument when it takes one", () => {
  assert.equal(pickedCommand(commands[0]), "/compact ");
  assert.equal(pickedCommand(commands[1]), "/context");
});

test("a message is a command when it starts with one", () => {
  assert.deepEqual(commandCall("/rename lab test"), { name: "rename", args: "lab test" });
  assert.deepEqual(commandCall("  /usage  "), { name: "usage", args: "" });
  assert.equal(commandCall("/"), null);
  assert.equal(commandCall("//comment"), null);
  assert.equal(commandCall("please /compact"), null);
});

test("a bare panel command is read off the terminal, and a bare /model opens the picker", () => {
  assert.deepEqual(commandSend("/usage", commands), { kind: "panel", name: "usage" });
  assert.deepEqual(commandSend("/mcp", commands), { kind: "panel", name: "mcp" });
  assert.deepEqual(commandSend("/mcp reconnect railway", commands), { kind: "message" });
  assert.deepEqual(commandSend("/model", commands), { kind: "model" });
  assert.deepEqual(commandSend("/model sonnet", commands), { kind: "message" });
  assert.deepEqual(commandSend("/compact", commands), { kind: "message" });
  assert.deepEqual(commandSend("hello", commands), { kind: "message" });
});

test("only a Claude the Mac types into takes commands", () => {
  assert.equal(takesCommands({ kind: "claude", inject: "tmux" }), true);
  assert.equal(takesCommands({ kind: "claude", inject: "hook" }), false);
  assert.equal(takesCommands({ kind: "codex", inject: "tmux" }), false);
  assert.equal(takesCommands(undefined), false);
});

test("the menu and a panel come from the Mac", async () => {
  const sent: Request[] = [];
  const ask = async (request: Request, timeoutMs?: number): Promise<DaemonEvent> => {
    sent.push(request);
    if (request.type === "list_commands") return { type: "commands", commands };
    assert.ok((timeoutMs ?? 0) >= 20_000, "a panel takes a few seconds to settle");
    return { type: "command_panel", panel: { command: request.text!, text: "Current week  68% used" } };
  };
  assert.equal((await listCommands(ask, "claude:s")).length, commands.length);
  const panel = await runPanel(ask, "claude:s", "usage");
  assert.equal(panel.text, "Current week  68% used");
  assert.deepEqual(sent.map((r) => [r.type, r.sessionId, r.text]), [
    ["list_commands", "claude:s", undefined],
    ["command_panel", "claude:s", "/usage"],
  ]);
});
