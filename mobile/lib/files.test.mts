import assert from "node:assert/strict";
import test from "node:test";

import { FILE_PART, fileSize, sendFile, withFiles } from "./files.ts";
import type { DaemonEvent, Request } from "./protocol.ts";

test("a file goes in parts, and the last answers with where the Mac keeps it", async () => {
  const sent: Request[] = [];
  const ask = async (request: Request): Promise<DaemonEvent> => {
    sent.push(request);
    const file = request.file!;
    const done = file.part === file.parts - 1;
    return { type: "attached", attached: { id: file.id, done, path: done ? "/Users/me/.agentman/files/s/AB/report.pdf" : undefined } };
  };
  const base64 = "Q".repeat(FILE_PART * 2 + 7);
  const path = await sendFile(ask, "claude:s", "f1", "report.pdf", base64);
  assert.equal(path, "/Users/me/.agentman/files/s/AB/report.pdf");
  assert.deepEqual(sent.map((r) => [r.type, r.sessionId, r.file?.part, r.file?.parts, r.file?.name]), [
    ["attach_file", "claude:s", 0, 3, "report.pdf"],
    ["attach_file", "claude:s", 1, 3, "report.pdf"],
    ["attach_file", "claude:s", 2, 3, "report.pdf"],
  ]);
  assert.equal(sent.map((r) => r.file!.data).join(""), base64);
});

test("the Mac's reason comes through when it can't keep a file", async () => {
  const ask = async (): Promise<DaemonEvent> => {
    throw new Error("that file is larger than 10 MB");
  };
  await assert.rejects(sendFile(ask, "claude:s", "f1", "big.bin", "AAAA"), /larger than 10 MB/);
});

test("files are named ahead of the words", () => {
  assert.equal(withFiles("Look at these", ["/m/a.pdf", "/m/b.log"]), "@/m/a.pdf @/m/b.log\n\nLook at these");
  assert.equal(withFiles("", ["/m/a.pdf"]), "@/m/a.pdf");
  assert.equal(withFiles("Just words", []), "Just words");
});

test("sizes read as a person would say them", () => {
  assert.equal(fileSize(512), "512 B");
  assert.equal(fileSize(2048), "2 KB");
  assert.equal(fileSize(2.4 * 1024 * 1024), "2.4 MB");
  assert.equal(fileSize(undefined), "");
});
