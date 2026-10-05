import assert from "node:assert/strict";
import test from "node:test";

import type { Question } from "./protocol.ts";
import { MAX_NOTE_CHARS, noteAnswer, submitAnswer, tapAnswer } from "./question-answer.ts";

const approval: Question = {
  id: "q",
  prompt: "Run rm -rf build?",
  options: [
    { key: "1", label: "Yes" },
    { key: "2", label: "Yes, and don't ask again" },
    { key: "3", label: "No, and tell Claude what to do differently", withText: true },
  ],
};

test("an ordinary option is one tap", () => {
  assert.deepEqual(tapAnswer(approval.options[0]), { optionKey: "1" });
  assert.deepEqual(tapAnswer(approval.options[1]), { optionKey: "2" });
});

test("an option that takes a note opens it instead of answering", () => {
  assert.equal(tapAnswer(approval.options[2]), null);
});

test("the note rides with its option", () => {
  assert.deepEqual(noteAnswer("3", "Use the staging database instead."), {
    optionKey: "3", answerText: "Use the staging database instead.",
  });
});

test("an empty note is the plain choice", () => {
  assert.deepEqual(noteAnswer("3", ""), { optionKey: "3" });
  assert.deepEqual(noteAnswer("3", "  \n\t "), { optionKey: "3" });
});

test("a note goes on one line, so a newline cannot submit half of it", () => {
  assert.deepEqual(noteAnswer("n", "  don't delete it.\n\nMove it to /tmp\r\nfirst  "), {
    optionKey: "n", answerText: "don't delete it. Move it to /tmp first",
  });
});

test("a note is bounded", () => {
  const answer = noteAnswer("n", "x".repeat(MAX_NOTE_CHARS + 50));
  assert.equal(answer.answerText?.length, MAX_NOTE_CHARS);
});

const form: Question = {
  id: "f",
  prompt: "Which database?",
  custom: true,
  options: [
    { key: "1", label: "Postgres", preview: "postgres://" },
    { key: "2", label: "None of these", withText: true },
  ],
};

test("the full form sends one chosen option, with its note when it takes one", () => {
  assert.deepEqual(submitAnswer(form, ["1"], ""), { optionKey: "1" });
  assert.deepEqual(submitAnswer(form, ["1"], "", "ignored"), { optionKey: "1" });
  assert.deepEqual(submitAnswer(form, ["2"], "", "SQLite in memory"), {
    optionKey: "2", answerText: "SQLite in memory",
  });
  assert.deepEqual(submitAnswer(form, ["2"], ""), { optionKey: "2" });
});

test("the full form keeps its custom answer and its multiple choice", () => {
  assert.deepEqual(submitAnswer(form, [], "MySQL"), { optionKeys: undefined, answerText: "MySQL" });
  assert.deepEqual(submitAnswer({ ...form, multiple: true }, ["1", "2"], "and Redis"), {
    optionKeys: ["1", "2"], answerText: "and Redis",
  });
});

test("the full form waits for an answer, and for only one when only one is allowed", () => {
  assert.equal(submitAnswer(form, [], "  "), null);
  assert.equal(submitAnswer(form, ["1"], "MySQL"), null);
  assert.equal(submitAnswer(form, ["1", "2"], ""), null);
});
