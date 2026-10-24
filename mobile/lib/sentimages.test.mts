import assert from "node:assert/strict";
import test from "node:test";

import { splitSentMessage } from "./sentimages.ts";

const DIR = "/Users/mac/.agentman/images/claude-s1";

test("the images typed ahead of a message are split off", () => {
  assert.deepEqual(
    splitSentMessage(`${DIR}/a1.png ${DIR}/b2.jpg why is this cut off?`),
    {
      images: [`${DIR}/a1.png`, `${DIR}/b2.jpg`],
      text: "why is this cut off?",
    },
  );
});

test("an image alone leaves no words", () => {
  assert.deepEqual(splitSentMessage(`${DIR}/c3.webp`), {
    images: [`${DIR}/c3.webp`],
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
