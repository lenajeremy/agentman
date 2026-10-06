import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

// The store hands every screen one memoized object. A piece of state the
// object reads but its dependency list leaves out never reaches a screen:
// the Notifications switches looked stuck on because notifyPrefs was missing,
// even though each tap saved the choice and sent it to the Mac.
//
// This reads store.tsx itself rather than rendering it, because the provider
// needs a relay connection, storage and notifications to mount at all.
const source = readFileSync(new URL("./store.tsx", import.meta.url), "utf8");

function storeMemo() {
  const start = source.indexOf("const store: Store = useMemo(");
  assert.notEqual(start, -1, "store.tsx no longer builds the store with useMemo; update this test");
  const rest = source.slice(start);
  const deps = /\n {4}\[([^\]]*)\],\n {2}\);/.exec(rest);
  assert.ok(deps, "could not find the store memo's dependency list");
  return { body: rest.slice(0, deps.index), deps: new Set(deps[1].split(",").map((d) => d.trim())) };
}

test("every piece of state the store hands out is in its dependency list", () => {
  const provider = source.slice(source.indexOf("export function StoreProvider"), source.indexOf("const store: Store = useMemo("));
  const states = [...provider.matchAll(/const \[(\w+), set\w+\] = useState/g)].map((m) => m[1]);
  assert.ok(states.includes("notifyPrefs"), "expected to find the store's state declarations");
  const { body, deps } = storeMemo();
  const missing = states.filter((name) => new RegExp(`\\b${name}\\b`).test(body) && !deps.has(name));
  assert.deepEqual(missing, [], `screens would never see changes to: ${missing.join(", ")}`);
});
