import type { AgentMessage } from "@agentman/protocol";
import {
  asArray,
  asRecord,
  asString,
  BoundedMap,
  clip,
  parseLine,
  summarizeToolInput,
  toEpoch,
  type ToolOutcome,
  type TranscriptParser,
} from "./shared.ts";

/**
 * Claude Code writes `~/.claude/projects/<cwd-slug>/<sessionId>.jsonl`.
 *
 * Only `user` and `assistant` records carry conversation. The rest of the file
 * is bookkeeping the UI has no use for — mode changes, generated titles,
 * file-history snapshots — and dropping it early is most of what makes the
 * mobile feed readable.
 *
 * None of this is a published format, so every field access is confined to
 * this file. When a Claude Code release moves something, this is the only
 * place that needs to change.
 */
const IGNORED_RECORD_TYPES = new Set([
  "mode",
  "permission-mode",
  "ai-title",
  "attachment",
  "file-history-snapshot",
  "file-history-delta",
  "last-prompt",
  "queue-operation",
  "system",
  "summary",
]);

/** Slash-command scaffolding that the CLI injects as if the user typed it. */
const COMMAND_WRAPPER = /^\s*<(command-name|command-message|command-args|local-command-stdout|user-prompt-submit-hook)/;

export class ClaudeParser implements TranscriptParser {
  readonly sessionId: string;
  /** Outcomes seen before their call — the backward-paging case. */
  #outcomes = new BoundedMap<ToolOutcome>();
  /** Names seen before their result — the live-tail case. */
  #toolNames = new BoundedMap<string>();

  constructor(sessionId: string) {
    this.sessionId = sessionId;
  }

  parse(text: string, offset: number): AgentMessage[] {
    const record = parseLine(text);
    if (!record) return [];

    const type = asString(record["type"]);
    if (type !== "user" && type !== "assistant") {
      if (type && !IGNORED_RECORD_TYPES.has(type)) {
        // Unknown record type: ignore, but keep the shape assertion in one
        // place so `am doctor` can flag drift after a CLI upgrade.
      }
      return [];
    }
    if (record["isMeta"] === true) return [];

    const message = asRecord(record["message"]);
    if (!message) return [];

    const uuid = asString(record["uuid"]) ?? `o${offset}`;
    const ts = toEpoch(record["timestamp"]);
    const isSidechain = record["isSidechain"] === true;
    const content = message["content"];

    // A plain string is a straight typed prompt.
    if (typeof content === "string") {
      const body = content.trim();
      if (!body || COMMAND_WRAPPER.test(body)) return [];
      return [
        {
          id: uuid,
          sessionId: this.sessionId,
          role: type,
          ts,
          text: body,
          ...(isSidechain ? { isSidechain } : {}),
        },
      ];
    }

    const out: AgentMessage[] = [];
    for (const [i, raw] of asArray(content).entries()) {
      const block = asRecord(raw);
      if (!block) continue;
      const kind = asString(block["type"]);

      if (kind === "text") {
        const body = (asString(block["text"]) ?? "").trim();
        if (!body || COMMAND_WRAPPER.test(body)) continue;
        out.push({
          id: `${uuid}:${i}`,
          sessionId: this.sessionId,
          role: type,
          ts,
          text: body,
          ...(isSidechain ? { isSidechain } : {}),
        });
        continue;
      }

      if (kind === "tool_use") {
        const id = asString(block["id"]) ?? `${uuid}:${i}`;
        const name = asString(block["name"]) ?? "tool";
        this.#toolNames.set(id, name);
        const summary = summarizeToolInput(name, asRecord(block["input"]));
        // Already-known outcome means we are paging backwards and met the
        // result first, so this row can be emitted complete in one go.
        const outcome = this.#outcomes.get(id);
        out.push({
          id,
          sessionId: this.sessionId,
          role: "tool",
          ts,
          ...(outcome?.preview ? { text: outcome.preview } : {}),
          tool: {
            name,
            ...(summary ? { summary } : {}),
            status: outcome?.status ?? "running",
          },
          ...(isSidechain ? { isSidechain } : {}),
        });
        continue;
      }

      if (kind === "tool_result") {
        const id = asString(block["tool_use_id"]);
        if (!id) continue;
        const outcome: ToolOutcome = {
          status: block["is_error"] === true ? "error" : "ok",
          preview: clip(flattenResult(block["content"])),
        };
        this.#outcomes.set(id, outcome);

        // Reading forwards the row already exists, so re-emit it under the
        // same id to settle it. The consumer upserts by id, so this replaces
        // the "running" row rather than adding a second one.
        const name = this.#toolNames.get(id);
        if (name) {
          out.push({
            id,
            sessionId: this.sessionId,
            role: "tool",
            ts,
            ...(outcome.preview ? { text: outcome.preview } : {}),
            tool: { name, status: outcome.status },
            ...(isSidechain ? { isSidechain } : {}),
          });
        }
        continue;
      }

      // `thinking` blocks are deliberately dropped: they are long, mostly
      // signature payload, and not what someone glancing at a phone wants.
    }

    return out;
  }
}

/** Tool results arrive as a string or as an array of content blocks. */
function flattenResult(content: unknown): string {
  if (typeof content === "string") return content;
  const parts: string[] = [];
  for (const raw of asArray(content)) {
    const block = asRecord(raw);
    if (!block) continue;
    const text = asString(block["text"]);
    if (text) parts.push(text);
    else if (asString(block["type"]) === "image") parts.push("[image]");
  }
  return parts.join("\n");
}
