import { test } from "node:test";
import assert from "node:assert/strict";

import { INSTALL_ID_KEY, installId } from "./install.ts";

function memory() {
  const values = new Map<string, string>();
  return {
    values,
    getItem: async (key: string) => values.get(key) ?? null,
    setItem: async (key: string, value: string) => {
      values.set(key, value);
    },
  };
}

// Named once and kept: every pairing from this install sends the same id.
test("an install is named once and keeps its name", async () => {
  const storage = memory();
  let made = 0;
  const random = () => {
    made++;
    return "6F1C2B9A-0D4E-4C1B-9A57-3E2F8D6B1C0A";
  };
  const first = await installId(storage, random);
  const again = await installId(storage, random);
  assert.equal(first, "install-6f1c2b9a0d4e4c1b9a573e2f8d6b1c0a");
  assert.equal(again, first);
  assert.equal(made, 1);
  assert.equal(storage.values.get(INSTALL_ID_KEY), first);
});

// Storage that fails still gives a usable id, just not a kept one.
test("storage that fails still names the install", async () => {
  const broken = {
    getItem: async () => {
      throw new Error("no storage");
    },
    setItem: async () => {
      throw new Error("no storage");
    },
  };
  assert.match(await installId(broken, () => "abc-def-0123456789"), /^install-abcdef0123456789$/);
});
