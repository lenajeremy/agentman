import { open, type FileHandle } from "node:fs/promises";

/**
 * Incremental JSONL reading — the piece that lets us keep zero server-side
 * storage. Agent transcripts are append-only JSONL on the user's own disk, so
 * we never copy them anywhere; we tail the end for live output and page
 * backwards through the file when the user scrolls.
 *
 * Two properties of append-only files make this safe, and the whole design
 * leans on them:
 *
 *   1. Byte offsets of existing content never change, so a cursor stays valid
 *      even while the agent is actively writing. Paging backwards from a fixed
 *      offset cannot be disturbed by appends.
 *   2. `\n` (0x0A) never appears inside a multi-byte UTF-8 sequence, so we can
 *      split on raw bytes and only decode once we hold a *complete* line. That
 *      sidesteps split-character corruption at chunk boundaries entirely —
 *      hence all the Buffer arithmetic below instead of string handling.
 */

const NEWLINE = 0x0a;
const DEFAULT_CHUNK = 64 * 1024;

/**
 * A single line can legitimately be enormous (a tool result holding a whole
 * file). We still cap it so a corrupt or binary file can't exhaust memory
 * while we scan for a newline that never comes.
 */
const MAX_LINE_BYTES = 32 * 1024 * 1024;

export interface BackwardOptions<T> {
  /**
   * Exclusive upper bound: only bytes in `[0, before)` are considered. Must be
   * a line-start offset — which is exactly what `nextCursor` hands back.
   * Omit to start from the end of the file.
   */
  before?: number;
  /** How many successfully mapped items to collect before stopping. */
  want: number;
  /**
   * Map one raw line to a domain object, or return null to skip it. Filtering
   * happens inside the read loop so a page is filled with *useful* items
   * rather than however many survive a post-filter.
   *
   * May return an array when one transcript record expands to several messages
   * (a single assistant turn often holds text plus multiple tool calls). Those
   * are counted individually against `want` and kept in chronological order.
   */
  map: (text: string, offset: number) => T | T[] | null;
  chunkSize?: number;
}

export interface BackwardResult<T> {
  /** Chronological order (oldest first), even though we read right-to-left. */
  items: T[];
  /** Start offset of the oldest item returned. Pass as `before` to continue. */
  nextCursor?: number;
  hasMore: boolean;
}

/**
 * Walk backwards from `before` collecting up to `want` mapped items.
 *
 * Reads fixed-size chunks right-to-left and only ever decodes complete lines.
 * A line straddling a chunk boundary is carried in `pending` (always
 * newline-terminated) and completed by the next chunk to its left.
 */
export async function collectBackward<T>(
  path: string,
  opts: BackwardOptions<T>,
): Promise<BackwardResult<T>> {
  const chunkSize = opts.chunkSize ?? DEFAULT_CHUNK;
  let handle: FileHandle | undefined;

  try {
    handle = await open(path, "r");
    const { size } = await handle.stat();

    // Clamp rather than throw: a cursor can outlive a rotated or truncated
    // transcript, and an empty page is a better answer than an error.
    let pos = Math.min(opts.before ?? size, size);

    const found: T[] = [];
    let oldestOffset: number | undefined;
    let pending = Buffer.alloc(0);

    while (pos > 0 && found.length < opts.want) {
      const readLen = Math.min(chunkSize, pos);
      const start = pos - readLen;
      const buf = Buffer.allocUnsafe(readLen);
      await handle.read(buf, 0, readLen, start);

      // `combined` covers absolute bytes [start, start + combined.length).
      const combined = pending.length > 0 ? Buffer.concat([buf, pending]) : buf;

      const newlines = indexNewlines(combined);

      if (newlines.length === 0) {
        // No line ends in this window: the whole thing is the middle of a very
        // long line. Carry it left and keep looking for its start.
        if (combined.length > MAX_LINE_BYTES) {
          throw new Error(
            `${path}: line exceeds ${MAX_LINE_BYTES} bytes at offset ${start} — refusing to buffer further`,
          );
        }
        pending = combined;
        pos = start;
        continue;
      }

      // Lines fully contained between two newlines, emitted right-to-left so we
      // can stop the moment the page is full.
      for (let j = newlines.length - 1; j >= 1 && found.length < opts.want; j--) {
        const from = newlines[j - 1]! + 1;
        const to = newlines[j]!;
        const abs = start + from;
        if (emit(opts.map(combined.toString("utf8", from, to), abs), found)) {
          oldestOffset = abs;
        }
      }

      // The bytes before the first newline belong to a line that begins further
      // left — unless we're at the head of the file, where it is complete.
      if (start === 0 && found.length < opts.want) {
        if (emit(opts.map(combined.toString("utf8", 0, newlines[0]!), 0), found)) {
          oldestOffset = 0;
        }
      }

      // Keep the newline so `pending` always ends on a line boundary; that
      // invariant is what guarantees the trailing region of the next
      // `combined` is empty, so we never re-emit or lose a line.
      pending = combined.subarray(0, newlines[0]! + 1);
      pos = start;
    }

    // Anything after the final newline on the first pass is a partial line the
    // agent is still writing. It is never emitted, so a half-written record
    // can't surface as corrupt output.

    return {
      items: found.reverse(),
      nextCursor: oldestOffset,
      hasMore: oldestOffset !== undefined && oldestOffset > 0,
    };
  } finally {
    await handle?.close();
  }
}

/**
 * Append a mapped result to the accumulator, returning whether anything landed.
 *
 * We are walking right-to-left, so a record expanding to several messages must
 * be pushed in reverse: the final `found.reverse()` then restores their true
 * chronological order relative to each other.
 */
function emit<T>(result: T | T[] | null, found: T[]): boolean {
  if (result === null || result === undefined) return false;
  if (Array.isArray(result)) {
    if (result.length === 0) return false;
    for (let k = result.length - 1; k >= 0; k--) found.push(result[k]!);
    return true;
  }
  found.push(result);
  return true;
}

function indexNewlines(buf: Buffer): number[] {
  const out: number[] = [];
  let i = buf.indexOf(NEWLINE);
  while (i !== -1) {
    out.push(i);
    i = buf.indexOf(NEWLINE, i + 1);
  }
  return out;
}

export interface TailedLine {
  text: string;
  /** Absolute byte offset of the line's first character. */
  offset: number;
}

/**
 * Forward tail over an append-only JSONL file.
 *
 * Holds a byte offset and a carry buffer for a trailing partial line, so
 * repeated calls to {@link read} yield each complete line exactly once no
 * matter how the writer chunks its output.
 */
export class JsonlTail {
  #offset = 0;
  #carry = Buffer.alloc(0);
  readonly path: string;

  constructor(path: string) {
    this.path = path;
  }

  get offset(): number {
    return this.#offset;
  }

  /**
   * Skip existing content and follow only what arrives from now on. Used when
   * attaching to a session that is already running and whose backlog the app
   * will pull on demand instead.
   */
  async seekToEnd(): Promise<void> {
    const handle = await open(this.path, "r");
    try {
      const { size } = await handle.stat();
      this.#offset = size;
      this.#carry = Buffer.alloc(0);
    } finally {
      await handle.close();
    }
  }

  /** Complete lines appended since the previous call. Empty when idle. */
  async read(): Promise<TailedLine[]> {
    let handle: FileHandle | undefined;
    try {
      handle = await open(this.path, "r");
      const { size } = await handle.stat();

      // Shrinkage means the file was truncated or replaced, so every offset we
      // hold is meaningless. Restart from the top rather than emit garbage.
      if (size < this.#offset) {
        this.#offset = 0;
        this.#carry = Buffer.alloc(0);
      }
      if (size === this.#offset) return [];

      const readLen = size - this.#offset;
      const buf = Buffer.allocUnsafe(readLen);
      await handle.read(buf, 0, readLen, this.#offset);

      const combined =
        this.#carry.length > 0 ? Buffer.concat([this.#carry, buf]) : buf;
      // Absolute position of combined[0], accounting for the carried fragment.
      const base = this.#offset - this.#carry.length;

      const lines: TailedLine[] = [];
      let lineStart = 0;
      let nl = combined.indexOf(NEWLINE);
      while (nl !== -1) {
        lines.push({
          text: combined.toString("utf8", lineStart, nl),
          offset: base + lineStart,
        });
        lineStart = nl + 1;
        nl = combined.indexOf(NEWLINE, lineStart);
      }

      const carry = combined.subarray(lineStart);
      if (carry.length > MAX_LINE_BYTES) {
        throw new Error(
          `${this.path}: unterminated line exceeds ${MAX_LINE_BYTES} bytes — refusing to buffer further`,
        );
      }
      this.#carry = Buffer.from(carry);
      this.#offset = size;

      return lines;
    } finally {
      await handle?.close();
    }
  }
}
