import assert from "node:assert/strict";
import test from "node:test";

import {
  DEFAULT_APPEARANCE,
  nativeScheme,
  parseAppearance,
  resolveScheme,
} from "./appearance-policy.ts";

test("light is the default, including when nothing has been stored", () => {
  assert.equal(DEFAULT_APPEARANCE, "light");
  assert.equal(parseAppearance(null), "light");
  assert.equal(parseAppearance(undefined), "light");
});

test("stored preferences are read back, and anything else is ignored", () => {
  assert.equal(parseAppearance("dark"), "dark");
  assert.equal(parseAppearance("system"), "system");
  assert.equal(parseAppearance("light"), "light");
  assert.equal(parseAppearance("Dark"), "light");
  assert.equal(parseAppearance('{"scheme":"dark"}'), "light");
});

test("an explicit choice wins over the phone's setting", () => {
  assert.equal(resolveScheme("light", "dark"), "light");
  assert.equal(resolveScheme("dark", "light"), "dark");
});

test("following the phone uses its scheme, and light when it reports none", () => {
  assert.equal(resolveScheme("system", "dark"), "dark");
  assert.equal(resolveScheme("system", "light"), "light");
  assert.equal(resolveScheme("system", null), "light");
  assert.equal(resolveScheme("system", "unspecified"), "light");
});

test("the OS is only handed back control when following the phone", () => {
  assert.equal(nativeScheme("light"), "light");
  assert.equal(nativeScheme("dark"), "dark");
  assert.equal(nativeScheme("system"), "unspecified");
});
