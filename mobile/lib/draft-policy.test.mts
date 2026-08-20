import assert from "node:assert/strict";
import test from "node:test";

import {
  draftNamespace,
  draftStorageKey,
  persistedDraftValue,
  createDraftPersistence,
} from "./draft-policy.ts";

function token(account: string, nonce: string): string {
  const claims = Buffer.from(JSON.stringify({ acc: account, jti: nonce }))
    .toString("base64url");
  return `${claims}.signature`;
}

test("drafts are isolated by relay account but survive device-token rotation", () => {
  const first = draftNamespace({ relayUrl: "https://relay.example", token: token("mac-a", "1") });
  const rotated = draftNamespace({ relayUrl: "https://relay.example", token: token("mac-a", "2") });
  const otherAccount = draftNamespace({ relayUrl: "https://relay.example", token: token("mac-b", "1") });
  const otherRelay = draftNamespace({ relayUrl: "https://other.example", token: token("mac-a", "1") });

  assert.equal(first, rotated);
  assert.notEqual(first, otherAccount);
  assert.notEqual(first, otherRelay);
  assert.equal(draftStorageKey(first, "claude:one"), draftStorageKey(rotated, "claude:one"));
  assert.notEqual(draftStorageKey(first, "claude:one"), draftStorageKey(first, "claude:two"));
});

test("draft save, isolation, and intentional clear use separate storage entries", async () => {
  const values = new Map<string, string>();
  const drafts = createDraftPersistence({
    async getItem(key) { return values.get(key) ?? null; },
    async setItem(key, value) { values.set(key, value); },
    async removeItem(key) { values.delete(key); },
  });

  await drafts.save("account-a", "session", "  keep my spacing  ");
  assert.equal(await drafts.load("account-a", "session"), "  keep my spacing  ");
  assert.equal(await drafts.load("account-b", "session"), "");
  await drafts.clear("account-a", "session");
  assert.equal(await drafts.load("account-a", "session"), "");
  await drafts.save("account-a", "session", "temporary");
  await drafts.save("account-a", "session", "  \n");
  assert.equal(await drafts.load("account-a", "session"), "");

  assert.equal(persistedDraftValue("  \n"), null);
  assert.equal(persistedDraftValue("  keep my spacing  "), "  keep my spacing  ");
});
