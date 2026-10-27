import assert from "node:assert/strict";
import test from "node:test";

import { sentMessageParts, splitSentMessage } from "./sentimages.ts";

const DIR = "/Users/mac/.agentman/images/claude-s1";

test("the images typed ahead of a message are split off", () => {
  assert.deepEqual(
    splitSentMessage(`${DIR}/a1.png ${DIR}/b2.jpg why is this cut off?`),
    {
      images: [`${DIR}/a1.png`, `${DIR}/b2.jpg`],
      files: [],
      text: "why is this cut off?",
    },
  );
});

test("an image alone leaves no words", () => {
  assert.deepEqual(splitSentMessage(`${DIR}/c3.webp`), {
    images: [`${DIR}/c3.webp`],
    files: [],
    text: "",
  });
});

test("other paths stay words", () => {
  assert.deepEqual(splitSentMessage("/etc/passwd.png look").images, []);
  assert.deepEqual(splitSentMessage(`${DIR}/../../secret.png x`).images, []);
  assert.deepEqual(splitSentMessage(`${DIR}/notes.txt x`).images, []);
  assert.equal(splitSentMessage(`see ${DIR}/a.png`).text, `see ${DIR}/a.png`);
  assert.equal(splitSentMessage("plain words").text, "plain words");
});

const FILES = "/Users/mac/.agentman/files/claude-s1";

test("files named ahead of a message are split off, the words after a blank line", () => {
  assert.deepEqual(splitSentMessage(`@${FILES}/AB12/report.pdf @${FILES}/CD34/run.log\n\nWhat failed?`), {
    images: [],
    files: [`${FILES}/AB12/report.pdf`, `${FILES}/CD34/run.log`],
    text: "What failed?",
  });
  assert.deepEqual(splitSentMessage(`@${FILES}/AB12/report.pdf`).files, [`${FILES}/AB12/report.pdf`]);
});

test("other @ words stay words", () => {
  assert.equal(splitSentMessage("@docs/fruit.md what is it?").text, "@docs/fruit.md what is it?");
  assert.deepEqual(splitSentMessage(`@${FILES}/../../x/secret.txt hi`).files, []);
  assert.deepEqual(splitSentMessage("@/etc/hosts hi").files, [], "a path without an extension stays words");
  assert.deepEqual(splitSentMessage("@/Users/me/Documents/Q3.pdf\n\nsummarize").files, ["/Users/me/Documents/Q3.pdf"],
    "a file attached at the Mac, named where it is");
});

test("pictures the agent recorded with a message lead it, wherever they are kept", () => {
  const pasted = "/var/folders/x8/T/codex-clipboard-IcwiTL.png";
  assert.deepEqual(sentMessageParts("why does it look like this?", [pasted]), {
    images: [pasted],
    files: [],
    text: "why does it look like this?",
  });
  assert.deepEqual(sentMessageParts(`${DIR}/a1.png look`, [pasted, `${DIR}/a1.png`]).images, [pasted, `${DIR}/a1.png`]);
  assert.deepEqual(sentMessageParts(`${DIR}/a1.png look`), splitSentMessage(`${DIR}/a1.png look`));
});
