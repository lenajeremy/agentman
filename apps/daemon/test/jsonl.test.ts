import { strict as assert } from "node:assert";
import { test } from "node:test";
import { mkdtemp, writeFile, appendFile, truncate } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { collectBackward, JsonlTail } from "../src/jsonl.ts";

async function fixture(lines: string[]): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), "agentman-jsonl-"));
  const path = join(dir, "t.jsonl");
  await writeFile(path, lines.length ? lines.join("\n") + "\n" : "");
  return path;
}

const asJson = (text: string, offset: number) => {
  try {
    return { ...(JSON.parse(text) as Record<string, unknown>), offset };
  } catch {
    return null;
  }
};

/* ---------------------------- backward paging ---------------------------- */

test("collectBackward returns the newest lines in chronological order", async () => {
  const path = await fixture(
    Array.from({ length: 10 }, (_, i) => JSON.stringify({ n: i })),
  );

  const page = await collectBackward(path, { want: 3, map: asJson });

  assert.deepEqual(page.items.map((i) => i.n), [7, 8, 9]);
  assert.equal(page.hasMore, true);
});

test("paging backwards is contiguous and never overlaps", async () => {
  const total = 250;
  const path = await fixture(
    Array.from({ length: total }, (_, i) => JSON.stringify({ n: i })),
  );

  const seen: number[] = [];
  let cursor: number | undefined;
  let guard = 0;

  for (;;) {
    const page: Awaited<ReturnType<typeof collectBackward<{ n: number }>>> =
      await collectBackward(path, {
        want: 40,
        before: cursor,
        map: asJson as (t: string, o: number) => { n: number } | null,
      });
    seen.unshift(...page.items.map((i) => i.n));
    if (!page.hasMore) break;
    cursor = page.nextCursor;
    assert.ok(++guard < 100, "paging failed to terminate");
  }

  // Every record exactly once, in order — the property that matters for scrollback.
  assert.deepEqual(seen, Array.from({ length: total }, (_, i) => i));
});

test("a page is filled with useful items, skipping noise lines", async () => {
  // Interleave records the parser rejects; a page should still come back full
  // rather than mostly empty after filtering.
  const lines: string[] = [];
  for (let i = 0; i < 100; i++) {
    lines.push(JSON.stringify({ noise: true }));
    lines.push(JSON.stringify({ n: i }));
  }
  const path = await fixture(lines);

  const page = await collectBackward<{ n: number }>(path, {
    want: 10,
    map: (text, offset) => {
      const v = asJson(text, offset) as { n?: number } | null;
      return typeof v?.n === "number" ? { n: v.n } : null;
    },
  });

  assert.equal(page.items.length, 10);
  assert.deepEqual(page.items.map((i) => i.n), [90, 91, 92, 93, 94, 95, 96, 97, 98, 99]);
});

test("lines straddling chunk boundaries survive intact", async () => {
  // Force many boundary crossings with a deliberately tiny chunk size.
  const path = await fixture(
    Array.from({ length: 60 }, (_, i) =>
      JSON.stringify({ n: i, pad: "x".repeat(i * 7) }),
    ),
  );

  const page = await collectBackward<{ n: number; pad: string }>(path, {
    want: 60,
    chunkSize: 16,
    map: asJson as (t: string, o: number) => { n: number; pad: string } | null,
  });

  assert.equal(page.items.length, 60);
  for (const [i, item] of page.items.entries()) {
    assert.equal(item.n, i);
    assert.equal(item.pad.length, i * 7, `payload corrupted on line ${i}`);
  }
});

test("multi-byte characters split across chunks are not corrupted", async () => {
  // The reason the reader works in bytes: naive string chunking mangles these.
  const text = "日本語のテキスト🎉 émoji";
  const path = await fixture(
    Array.from({ length: 40 }, (_, i) => JSON.stringify({ n: i, text })),
  );

  const page = await collectBackward<{ n: number; text: string }>(path, {
    want: 40,
    chunkSize: 7, // small enough to split characters constantly
    map: asJson as (t: string, o: number) => { n: number; text: string } | null,
  });

  assert.equal(page.items.length, 40);
  for (const item of page.items) assert.equal(item.text, text);
});

test("a single line larger than the chunk size is reassembled", async () => {
  const big = "y".repeat(200_000);
  const path = await fixture([
    JSON.stringify({ n: 0 }),
    JSON.stringify({ n: 1, big }),
    JSON.stringify({ n: 2 }),
  ]);

  const page = await collectBackward<{ n: number; big?: string }>(path, {
    want: 3,
    chunkSize: 4096,
    map: asJson as (t: string, o: number) => { n: number; big?: string } | null,
  });

  assert.deepEqual(page.items.map((i) => i.n), [0, 1, 2]);
  assert.equal(page.items[1]?.big?.length, 200_000);
});

test("a half-written trailing line is never emitted", async () => {
  const path = await fixture([JSON.stringify({ n: 0 }), JSON.stringify({ n: 1 })]);
  await appendFile(path, '{"n":2,"incomp'); // agent mid-write

  const page = await collectBackward<{ n: number }>(path, {
    want: 10,
    map: asJson as (t: string, o: number) => { n: number } | null,
  });

  assert.deepEqual(page.items.map((i) => i.n), [0, 1]);
});

test("appends during backward paging cannot disturb an in-flight cursor", async () => {
  const path = await fixture(
    Array.from({ length: 100 }, (_, i) => JSON.stringify({ n: i })),
  );

  const first = await collectBackward<{ n: number }>(path, {
    want: 20,
    map: asJson as (t: string, o: number) => { n: number } | null,
  });
  assert.deepEqual(first.items.map((i) => i.n), Array.from({ length: 20 }, (_, i) => 80 + i));

  // The agent keeps working while the user scrolls up.
  await appendFile(
    path,
    Array.from({ length: 5 }, (_, i) => JSON.stringify({ n: 100 + i })).join("\n") + "\n",
  );

  const second = await collectBackward<{ n: number }>(path, {
    want: 20,
    before: first.nextCursor,
    map: asJson as (t: string, o: number) => { n: number } | null,
  });

  // Still the 20 records before the cursor: no skips, no duplicates.
  assert.deepEqual(second.items.map((i) => i.n), Array.from({ length: 20 }, (_, i) => 60 + i));
});

test("empty and single-line files behave", async () => {
  const empty = await collectBackward(await fixture([]), { want: 5, map: asJson });
  assert.deepEqual(empty.items, []);
  assert.equal(empty.hasMore, false);

  const one = await collectBackward(await fixture([JSON.stringify({ n: 0 })]), {
    want: 5,
    map: asJson,
  });
  assert.equal(one.items.length, 1);
  assert.equal(one.hasMore, false, "reaching the head of the file means no more pages");
});

test("reading does not scan the whole file for a small page", async () => {
  // Guards the property the plan depends on: opening a huge transcript must not
  // cost a full read. 40MB file, one small page.
  const lines = Array.from({ length: 40_000 }, (_, i) =>
    JSON.stringify({ n: i, pad: "z".repeat(1000) }),
  );
  const path = await fixture(lines);

  // Count bytes actually pulled off disk rather than timing the call — wall
  // clock here is dominated by page-cache effects from writing the fixture.
  let bytesRead = 0;
  const page = await collectBackward<{ n: number }>(path, {
    want: 10,
    map: (text, offset) => {
      bytesRead += text.length;
      return asJson(text, offset) as { n: number } | null;
    },
  });

  assert.equal(page.items.length, 10);
  assert.equal(page.items.at(-1)?.n, 39_999);
  // ~10 lines of ~1KB, not the 40MB the file holds.
  assert.ok(
    bytesRead < 256 * 1024,
    `expected to touch only the tail, decoded ${bytesRead} bytes of a 40MB file`,
  );
});

/* ------------------------------- forward tail ---------------------------- */

test("JsonlTail yields each complete line exactly once", async () => {
  const path = await fixture([JSON.stringify({ n: 0 })]);
  const tail = new JsonlTail(path);

  assert.deepEqual((await tail.read()).map((l) => JSON.parse(l.text).n), [0]);
  assert.deepEqual(await tail.read(), [], "idle reads yield nothing");

  await appendFile(path, JSON.stringify({ n: 1 }) + "\n" + JSON.stringify({ n: 2 }) + "\n");
  assert.deepEqual((await tail.read()).map((l) => JSON.parse(l.text).n), [1, 2]);
});

test("JsonlTail holds back a partial line until its newline arrives", async () => {
  const path = await fixture([]);
  const tail = new JsonlTail(path);

  await appendFile(path, '{"n":0}\n{"n":');
  assert.deepEqual(
    (await tail.read()).map((l) => JSON.parse(l.text).n),
    [0],
    "the partial second line must be withheld",
  );

  await appendFile(path, '1}\n');
  assert.deepEqual((await tail.read()).map((l) => JSON.parse(l.text).n), [1]);
});

test("JsonlTail reports offsets that work as backward cursors", async () => {
  const path = await fixture(
    Array.from({ length: 5 }, (_, i) => JSON.stringify({ n: i })),
  );
  const tail = new JsonlTail(path);
  const lines = await tail.read();

  // Feed a tailed line's offset straight back in as a page boundary.
  const page = await collectBackward<{ n: number }>(path, {
    want: 10,
    before: lines[3]!.offset,
    map: asJson as (t: string, o: number) => { n: number } | null,
  });
  assert.deepEqual(page.items.map((i) => i.n), [0, 1, 2]);
});

test("JsonlTail restarts cleanly when the file is truncated", async () => {
  const path = await fixture([JSON.stringify({ n: 0 }), JSON.stringify({ n: 1 })]);
  const tail = new JsonlTail(path);
  await tail.read();

  await truncate(path, 0);
  await appendFile(path, JSON.stringify({ n: 9 }) + "\n");

  assert.deepEqual(
    (await tail.read()).map((l) => JSON.parse(l.text).n),
    [9],
    "a shrunken file must reset rather than emit garbage",
  );
});

test("JsonlTail.seekToEnd skips the backlog", async () => {
  const path = await fixture(
    Array.from({ length: 100 }, (_, i) => JSON.stringify({ n: i })),
  );
  const tail = new JsonlTail(path);
  await tail.seekToEnd();

  assert.deepEqual(await tail.read(), []);
  await appendFile(path, JSON.stringify({ n: 100 }) + "\n");
  assert.deepEqual((await tail.read()).map((l) => JSON.parse(l.text).n), [100]);
});
