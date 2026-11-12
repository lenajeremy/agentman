// node --test web/src/shell/folderChoices.test.mts (Node strips the types).
import { test } from "node:test";
import assert from "node:assert/strict";
import { candidates, isAppPlace, place, remember, shown, SHOWN, KEPT } from "./folderChoices.ts";

const home = "/Users/me";
const folder = (path: string, at: number) => ({ path, agents: 1, lastActivityAt: at * 1000 });

test("folders newest first, the user's chosen ones among them, an app's own places left out", () => {
  const folders = [folder("/Users/me/a", 10), folder("/Users/me/b", 30), folder("/private/var/folders/x/T/c", 50), folder("/Users/me/Library/Caches/d", 60), folder("/tmp/e", 70)];
  assert.deepEqual(candidates(folders, { "/Users/me/z": 20, "/Users/me/a": 40 }, home), ["/Users/me/a", "/Users/me/b", "/Users/me/z"]);
});

test("those gone are left out, at most eight shown, and the one filtered to always", () => {
  const paths = Array.from({ length: 12 }, (_, i) => `/Users/me/p${i}`);
  const gone = new Set(["/Users/me/p1"]);
  const out = shown(paths, (p) => !gone.has(p), null);
  assert.equal(out.length, SHOWN);
  assert.ok(!out.includes("/Users/me/p1"));
  assert.equal(shown(paths, () => true, "/Users/me/p11")[0], "/Users/me/p11");
  assert.equal(shown(paths, () => true, "/Users/me/p0").length, SHOWN);
});

test("where a folder is, from home", () => {
  assert.equal(place("/Users/me/projects/demo", home), "~/projects");
  assert.equal(place("/Users/me/demo", home), "~");
  assert.equal(place("/srv/work/demo", home), "/srv/work");
  assert.equal(isAppPlace("/var/folders/x/T/a", home), true);
  assert.equal(isAppPlace("/Users/me/Library/x", home), true);
  assert.equal(isAppPlace("/Users/me/work", home), false);
});

test("chosen folders: the newest ten kept", () => {
  let chosen = {};
  for (let i = 0; i < 12; i++) chosen = remember(chosen, `/w/${i}`, i);
  assert.equal(Object.keys(chosen).length, KEPT);
  assert.ok(!("/w/0" in chosen) && !("/w/1" in chosen) && "/w/11" in chosen);
});
