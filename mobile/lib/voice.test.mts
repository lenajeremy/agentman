import assert from "node:assert/strict";
import test from "node:test";

import type { DaemonEvent, Request } from "./protocol.ts";
import { addSpoken, transcribe, VOICE_PART, voiceParts } from "./voice.ts";

test("a recording goes in parts small enough for the relay, and joins back whole", () => {
  const base64 = "A".repeat(VOICE_PART * 2 + 10);
  const parts = voiceParts(base64);
  assert.equal(parts.length, 3);
  assert.ok(parts.every((part) => part.length <= VOICE_PART));
  assert.equal(parts.join(""), base64);
  assert.deepEqual(voiceParts(""), []);
});

test("every part is sent in order, and the last one answers with the words", async () => {
  const sent: Request[] = [];
  const ask = async (request: Request): Promise<DaemonEvent> => {
    sent.push(request);
    const voice = request.voice!;
    const done = voice.part === voice.parts - 1;
    return { type: "transcript", transcript: { id: voice.id, done, text: done ? "Run the tests." : undefined } };
  };
  const text = await transcribe(ask, "B".repeat(VOICE_PART + 1), "rec-1");
  assert.equal(text, "Run the tests.");
  assert.deepEqual(sent.map((request) => [request.type, request.voice?.part, request.voice?.parts, request.voice?.mime]),
    [["transcribe", 0, 2, "audio/wav"], ["transcribe", 1, 2, "audio/wav"]]);
});

test("the Mac's reason comes through when it can't write it out", async () => {
  const ask = async (): Promise<DaemonEvent> => {
    throw new Error("Voice prompts are written out by Celery: sign in to Celery in Agentman's settings on your Mac (Keys).");
  };
  await assert.rejects(transcribe(ask, "CCCC", "rec-2"), /sign in to Celery/);
  await assert.rejects(transcribe(ask, "", "rec-3"), /Nothing was recorded/);
});

test("spoken words join what is already typed", () => {
  assert.equal(addSpoken("", "Hello."), "Hello.");
  assert.equal(addSpoken("Fix the build ", "And run the tests."), "Fix the build And run the tests.");
});
