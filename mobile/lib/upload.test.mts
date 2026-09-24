import assert from "node:assert/strict";
import test from "node:test";

import { uploadImage } from "./upload.ts";

interface Call {
  url: string;
  init: RequestInit;
}

function stubFetch(handler: (call: Call) => Response): Call[] {
  const calls: Call[] = [];
  globalThis.fetch = (async (url: string | URL | Request, init: RequestInit = {}) => {
    const call = { url: String(url), init };
    calls.push(call);
    return handler(call);
  }) as typeof fetch;
  return calls;
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

test("the image is posted to the relay's upload endpoint with the token", async () => {
  const calls = stubFetch(() => json({ id: "TICKET" }));
  const id = await uploadImage("https://agentman.online", "device-token", "AAAA");

  assert.equal(id, "TICKET");
  assert.equal(calls[0].url, "https://agentman.online/upload");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(
    (calls[0].init.headers as Record<string, string>).Authorization,
    "Bearer device-token",
  );
});

// A relay url stored with a trailing slash must not produce "//upload".
test("a trailing slash on the relay url is tolerated", async () => {
  const calls = stubFetch(() => json({ id: "T" }));
  await uploadImage("https://agentman.online///", "t", "AAAA");
  assert.equal(calls[0].url, "https://agentman.online/upload");
});

// Raw bytes, not base64: base64 in the body would inflate it by a third, and
// the relay identifies the image from its own bytes.
test("the body is the decoded bytes", async () => {
  const calls = stubFetch(() => json({ id: "T" }));
  // "AAECAw==" is 0x00 0x01 0x02 0x03.
  await uploadImage("https://relay", "t", "AAECAw==");
  const body = calls[0].init.body as unknown as Uint8Array;
  assert.deepEqual(Array.from(body), [0, 1, 2, 3]);
});

test("the relay's own message is what the user is told", async () => {
  stubFetch(() => json({ error: "too many images in flight" }, 507));
  await assert.rejects(uploadImage("https://relay", "t", "AAAA"), /too many images in flight/);
});

test("a failure with no message still fails clearly", async () => {
  stubFetch(() => new Response("nope", { status: 500 }));
  await assert.rejects(uploadImage("https://relay", "t", "AAAA"), /could not be sent/);
});

// A success that carries no ticket is a failure, not an empty string handed on
// to send_message.
test("a response without an id is refused", async () => {
  stubFetch(() => json({ id: "" }));
  await assert.rejects(uploadImage("https://relay", "t", "AAAA"), /upload id/);
  stubFetch(() => json({}));
  await assert.rejects(uploadImage("https://relay", "t", "AAAA"), /upload id/);
});
