import assert from "node:assert/strict";
import { test } from "node:test";

import {
  base64Bytes,
  describeSource,
  fetchOriginal,
  OriginalChangedError,
  saveName,
} from "./original.ts";
import { decodeDaemonEvent, type WorkspaceResult } from "./protocol.ts";

const PIECE = 1024;

/** A Mac serving `file` in pieces, the way the daemon does. */
function mac(file: Buffer, version = "v1") {
  const asked: number[] = [];
  return {
    asked,
    read: async (offset: number): Promise<WorkspaceResult> => {
      asked.push(offset);
      return {
        kind: "chunk",
        sessionId: "claude:one",
        mime: "image/png",
        data: file.subarray(offset, offset + PIECE).toString("base64"),
        offset,
        size: file.length,
        version,
      };
    },
  };
}

/** A file on the phone, written the way expo-file-system appends. */
function disk() {
  let contents = Buffer.alloc(0);
  return {
    write: (data: string, first: boolean) => {
      const piece = Buffer.from(data, "base64");
      contents = first ? piece : Buffer.concat([contents, piece]);
    },
    get contents() {
      return contents;
    },
  };
}

function picture(length: number): Buffer {
  const bytes = Buffer.alloc(length);
  for (let i = 0; i < length; i++) bytes[i] = (i * 31 + 7) % 256;
  return bytes;
}

test("a file arrives whole, in order, whatever its length", async () => {
  // One byte short of a piece, exactly a piece, one over, and several with a
  // ragged end: the boundaries where an off-by-one would drop or repeat bytes.
  for (const length of [1, PIECE - 1, PIECE, PIECE + 1, PIECE * 3 + 517]) {
    const file = picture(length);
    const from = mac(file);
    const to = disk();
    const progress: number[] = [];
    const result = await fetchOriginal({
      read: from.read,
      write: to.write,
      onProgress: (received) => progress.push(received),
    });
    assert.deepEqual(result, { size: length, mime: "image/png" });
    assert.ok(to.contents.equals(file), `${length} bytes did not survive the trip`);
    assert.equal(progress.at(-1), length);
    // Each piece is asked for from where the last one ended, and none twice.
    assert.deepEqual(from.asked, Array.from({ length: Math.ceil(length / PIECE) }, (_, i) => i * PIECE));
  }
});

test("a file rewritten mid-download is refused, not stitched together", async () => {
  const before = picture(PIECE * 3);
  const after = Buffer.from(before).fill(0xee);
  const to = disk();
  let calls = 0;
  await assert.rejects(
    fetchOriginal({
      // The agent overwrites the screenshot after the first piece is read.
      read: (offset) => (calls++ === 0 ? mac(before, "v1") : mac(after, "v2")).read(offset),
      write: to.write,
    }),
    OriginalChangedError,
  );
  assert.equal(to.contents.length, PIECE, "a piece of the second file was written after the first");
});

test("a file that changes length is refused even if its stamp does not", async () => {
  const before = picture(PIECE * 3);
  const longer = picture(PIECE * 4);
  let calls = 0;
  await assert.rejects(
    fetchOriginal({
      read: (offset) => (calls++ === 0 ? mac(before) : mac(longer)).read(offset),
      write: () => {},
    }),
    OriginalChangedError,
  );
});

test("a piece that starts somewhere else, or is no piece at all, stops the download", async () => {
  const file = picture(PIECE * 2);
  const from = mac(file);
  await assert.rejects(
    fetchOriginal({
      read: async (offset) => ({ ...(await from.read(offset)), offset: offset + 1 }),
      write: () => {},
    }),
    /out of order/,
  );
  await assert.rejects(
    fetchOriginal({
      read: async () => ({ kind: "file", sessionId: "claude:one", mime: "image/png", image: "aGVsbG8=" }),
      write: () => {},
    }),
    /not part of the image/,
  );
  // An empty piece would otherwise ask for the same offset forever.
  await assert.rejects(
    fetchOriginal({
      read: async (offset) => ({ ...(await from.read(offset)), data: "" }),
      write: () => {},
    }),
    /not part of the image/,
  );
  // More bytes than the file was said to hold.
  await assert.rejects(
    fetchOriginal({
      read: async (offset) => ({ ...(await from.read(offset)), size: PIECE / 2 }),
      write: () => {},
    }),
    OriginalChangedError,
  );
});

test("a file over the limit is refused before anything is written", async () => {
  let wrote = false;
  await assert.rejects(
    fetchOriginal({
      read: async (offset) => ({ ...(await mac(picture(PIECE)).read(offset)), size: 33 * 1024 * 1024 }),
      write: () => {
        wrote = true;
      },
    }),
    /too large/,
  );
  assert.equal(wrote, false);
});

test("leaving the screen stops the download between pieces", async () => {
  const from = mac(picture(PIECE * 5));
  const to = disk();
  let pieces = 0;
  const result = await fetchOriginal({
    read: from.read,
    write: (data, first) => {
      pieces++;
      to.write(data, first);
    },
    cancelled: () => pieces >= 2,
  });
  assert.equal(result, null);
  assert.equal(pieces, 2);
  assert.deepEqual(from.asked, [0, PIECE]);
});

test("base64 length is counted without decoding", () => {
  for (const length of [1, 2, 3, 4, 5, 1023, 1024, 1025]) {
    assert.equal(base64Bytes(picture(length).toString("base64")), length);
  }
  assert.equal(base64Bytes(""), 0);
  assert.equal(base64Bytes("abc"), 0, "a truncated string is not a piece");
});

test("the button says what saving brings, and nothing when that is unknown", () => {
  assert.equal(
    describeSource({ size: 4_404_019, mime: "image/png", width: 3024, height: 1964, reduced: true }),
    "3024 × 1964 · 4.2 MB",
  );
  // WebP: the Mac cannot read its dimensions.
  assert.equal(describeSource({ size: 300_000, mime: "image/webp" }), "293 KB");
  // A Mac too old to describe the file.
  assert.equal(describeSource(undefined), "");
});

test("a saved file is named for what it holds", () => {
  assert.equal(saveName("/tmp/shots/home screen.png", "image/png"), "home screen.png");
  // A preview of a PNG is often a JPEG; the name must not say otherwise.
  assert.equal(saveName("/tmp/shot.png", "image/jpeg"), "shot.jpg");
  assert.equal(saveName("Screenshot 2026-09-30 at 10.41.22.png", "image/png"),
    "Screenshot 2026-09-30 at 10.41.22.png");
  // The name comes from another machine: nothing that could steer a path.
  assert.equal(saveName("/tmp/..", "image/png"), "image.png");
  assert.equal(saveName("/tmp/a\\b:c*d.png", "image/png"), "a_b_c_d.png");
  assert.equal(saveName("", "image/gif"), "image.gif");
  assert.equal(saveName("/tmp/.hidden.png", "image/png"), "hidden.png");
});

test("the wire format accepts a piece and a described preview, and rejects malformed ones", () => {
  const chunk = {
    kind: "chunk", sessionId: "claude:one", path: "shot.png", mime: "image/png",
    data: "aGVsbG8=", size: 5, version: "5-abc",
  };
  // Offset zero is omitted by the Mac, so its absence must be accepted.
  assert.ok(decodeDaemonEvent({ type: "workspace", workspace: chunk }));
  assert.ok(decodeDaemonEvent({ type: "workspace", workspace: { ...chunk, offset: 1024, size: 4096 } }));
  for (const broken of [
    { data: "" },
    { data: "not base64!" },
    { data: undefined },
    { mime: "text/html" },
    { size: 0 },
    { size: 33 * 1024 * 1024 },
    { offset: -1 },
    { offset: 1.5 },
    { version: "" },
    { version: undefined },
  ]) {
    assert.equal(
      decodeDaemonEvent({ type: "workspace", workspace: { ...chunk, ...broken } }),
      null,
      `accepted a piece with ${JSON.stringify(broken)}`,
    );
  }

  const preview = {
    kind: "file", sessionId: "claude:one", path: "shot.png", mime: "image/jpeg", image: "aGVsbG8=",
  };
  const source = { size: 4_404_019, mime: "image/png", width: 3024, height: 1964, reduced: true };
  const decoded = decodeDaemonEvent({ type: "workspace", workspace: { ...preview, source } });
  assert.deepEqual(decoded?.workspace?.source, source);
  // A Mac too old to describe the file still previews.
  assert.ok(decodeDaemonEvent({ type: "workspace", workspace: preview }));
  for (const broken of [
    { size: -1 }, { size: "big" }, { mime: "application/pdf" }, { width: -5 }, { reduced: "yes" },
  ]) {
    assert.equal(
      decodeDaemonEvent({ type: "workspace", workspace: { ...preview, source: { ...source, ...broken } } }),
      null,
      `accepted a source with ${JSON.stringify(broken)}`,
    );
  }
  // Bytes have no business on anything but a piece.
  assert.equal(decodeDaemonEvent({ type: "workspace", workspace: { ...preview, data: "aGVsbG8=" } }), null);
});
