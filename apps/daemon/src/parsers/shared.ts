import type { AgentMessage } from "@agentman/protocol";

/**
 * A phone screen is a hostile place for a 40KB tool result. Everything the
 * parsers surface is clipped to something readable at a glance; the full
 * content stays on disk where the user can still reach it from a terminal.
 */
export const PREVIEW_CHARS = 400;
export const SUMMARY_CHARS = 160;

export function clip(text: string, max = PREVIEW_CHARS): string {
  const flat = text.replace(/\s+/g, " ").trim();
  return flat.length <= max ? flat : flat.slice(0, max - 1) + "…";
}

/** Parse a line, returning null rather than throwing on a malformed record. */
export function parseLine(text: string): Record<string, unknown> | null {
  if (text.length === 0) return null;
  try {
    const value: unknown = JSON.parse(text);
    return typeof value === "object" && value !== null
      ? (value as Record<string, unknown>)
      : null;
  } catch {
    return null;
  }
}

export function toEpoch(value: unknown, fallback = 0): number {
  if (typeof value === "number") return value;
  if (typeof value === "string") {
    const t = Date.parse(value);
    if (!Number.isNaN(t)) return t;
  }
  return fallback;
}

export function asString(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

export function asRecord(value: unknown): Record<string, unknown> | undefined {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}

export function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

/**
 * Reduce a tool invocation to the one detail a human scanning a feed wants:
 * the command, the path, the pattern. Falls back to compact JSON so an unknown
 * tool still shows something useful rather than a bare name.
 */
export function summarizeToolInput(
  name: string,
  input: Record<string, unknown> | undefined,
): string | undefined {
  if (!input) return undefined;

  const pick = (...keys: string[]): string | undefined => {
    for (const key of keys) {
      const v = input[key];
      if (typeof v === "string" && v.trim()) return v;
    }
    return undefined;
  };

  switch (name) {
    case "Bash":
    case "BashOutput":
      return clip(pick("command", "description") ?? "", SUMMARY_CHARS) || undefined;
    case "Read":
    case "Write":
    case "NotebookEdit":
      return pick("file_path", "notebook_path");
    case "Edit":
      return pick("file_path");
    case "Glob":
    case "Grep":
      return pick("pattern");
    case "WebFetch":
      return pick("url");
    case "WebSearch":
      return pick("query");
    case "Task":
    case "Agent":
      return clip(pick("description", "prompt") ?? "", SUMMARY_CHARS) || undefined;
    case "TodoWrite":
      return undefined;
    default: {
      const direct = pick("command", "file_path", "path", "query", "pattern", "url");
      if (direct) return clip(direct, SUMMARY_CHARS);
      try {
        return clip(JSON.stringify(input), SUMMARY_CHARS);
      } catch {
        return undefined;
      }
    }
  }
}

/**
 * Bounded lookup used to pair tool calls with their results.
 *
 * The pairing works in both read directions, which is what keeps a tool call
 * rendering as a single row rather than two: reading forwards we meet the call
 * first and fill in the outcome later; reading backwards (paging through
 * history) we meet the result first and attach it when the call shows up.
 * Either way the map is the only state involved, and it is capped so a long
 * session cannot grow it without bound.
 */
export class BoundedMap<V> {
  #map = new Map<string, V>();
  #limit: number;

  constructor(limit = 2000) {
    this.#limit = limit;
  }

  set(key: string, value: V): void {
    if (this.#map.size >= this.#limit) {
      const oldest = this.#map.keys().next();
      if (!oldest.done) this.#map.delete(oldest.value);
    }
    this.#map.set(key, value);
  }

  get(key: string): V | undefined {
    return this.#map.get(key);
  }

  clear(): void {
    this.#map.clear();
  }
}

export type ToolOutcome = { status: "ok" | "error"; preview?: string };

/** Shape every source adapter's transcript parser conforms to. */
export interface TranscriptParser {
  /**
   * Turn one raw JSONL line into zero or more normalized messages.
   * `offset` is the line's byte position, used to mint stable ids for records
   * that carry no identifier of their own.
   */
  parse(text: string, offset: number): AgentMessage[];
}
