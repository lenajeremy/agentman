import type { AgentMessage, SessionState } from "@agentman/protocol";
import {
  asArray,
  asRecord,
  asString,
  clip,
  parseLine,
  SUMMARY_CHARS,
  toEpoch,
  type TranscriptParser,
} from "./shared.ts";

/**
 * Codex writes `~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl`.
 *
 * The file interleaves two parallel streams: `response_item` (the raw history
 * sent to the model) and `event_msg` (the semantic stream the TUI renders).
 * They overlap almost exactly — on a sample session, 175 `AgentMessage` events
 * against 175 `assistant` response items — so reading both would duplicate
 * every message.
 *
 * We read `event_msg`/`item_completed`, because it is the better of the two:
 * it excludes the `developer` role (injected instructions the user never
 * wrote), and it reports commands already parsed and file changes already
 * resolved to paths, rather than as raw tool-call payloads.
 */
export class CodexParser implements TranscriptParser {
  readonly sessionId: string;

  constructor(sessionId: string) {
    this.sessionId = sessionId;
  }

  parse(text: string, offset: number): AgentMessage[] {
    const record = parseLine(text);
    if (!record) return [];
    if (asString(record["type"]) !== "event_msg") return [];

    const payload = asRecord(record["payload"]);
    if (!payload || asString(payload["type"]) !== "item_completed") return [];

    const item = asRecord(payload["item"]);
    if (!item) return [];

    const ts = toEpoch(record["timestamp"]);
    const id = asString(item["id"]) ?? `o${offset}`;
    const base = { sessionId: this.sessionId, ts } as const;

    switch (asString(item["type"])) {
      case "UserMessage": {
        const body = collectText(item["content"]);
        return body ? [{ id, ...base, role: "user", text: body }] : [];
      }

      case "AgentMessage": {
        const body = collectText(item["content"]);
        return body ? [{ id, ...base, role: "assistant", text: body }] : [];
      }

      case "CommandExecution": {
        // `parsed_cmd` is Codex's own readable rendering of the command;
        // `command` is the raw argv, which is usually a shell wrapper.
        const parsed = asRecord(asArray(item["parsed_cmd"])[0]);
        const command =
          asString(parsed?.["cmd"]) ??
          asArray(item["command"]).filter((c): c is string => typeof c === "string").at(-1) ??
          "";
        const status = asString(item["status"]);
        const exitCode = item["exit_code"];
        const failed =
          status === "failed" || (typeof exitCode === "number" && exitCode !== 0);
        const output = asString(item["aggregated_output"]) ?? asString(item["stdout"]);
        return [
          {
            id,
            ...base,
            role: "tool",
            ...(output ? { text: clip(output) } : {}),
            tool: {
              name: "Shell",
              ...(command ? { summary: clip(command, SUMMARY_CHARS) } : {}),
              status: status === "in_progress" ? "running" : failed ? "error" : "ok",
            },
          },
        ];
      }

      case "FileChange": {
        const changes = asRecord(item["changes"]) ?? {};
        const paths = Object.keys(changes);
        const summary =
          paths.length === 1
            ? paths[0]!
            : `${paths.length} files: ${paths.slice(0, 3).map(basename).join(", ")}`;
        return [
          {
            id,
            ...base,
            role: "tool",
            tool: { name: "Edit", summary: clip(summary, SUMMARY_CHARS), status: "ok" },
          },
        ];
      }

      case "McpToolCall": {
        const server = asString(item["server"]) ?? "mcp";
        const tool = asString(item["tool"]) ?? "call";
        return [
          {
            id,
            ...base,
            role: "tool",
            tool: {
              name: `${server}.${tool}`,
              status: asString(item["status"]) === "failed" ? "error" : "ok",
            },
          },
        ];
      }

      case "ContextCompaction":
        return [{ id, ...base, role: "system", text: "Context compacted" }];

      // `Reasoning` is dropped for the same reason as Claude's `thinking`
      // blocks: long, largely opaque, and not what a glance at a phone is for.
      default:
        return [];
    }
  }
}

/**
 * Turn-level state transitions, read from the same `event_msg` stream. Used to
 * drive the busy/idle dot when hooks are not installed.
 */
export function codexStateFromLine(text: string): SessionState | undefined {
  const record = parseLine(text);
  if (!record || asString(record["type"]) !== "event_msg") return undefined;
  const payload = asRecord(record["payload"]);
  switch (asString(payload?.["type"])) {
    case "task_started":
      return "busy";
    case "task_complete":
    case "turn_aborted":
      return "idle";
    default:
      return undefined;
  }
}

/** Content blocks use `text` in events and `Text` in items; accept both. */
function collectText(content: unknown): string {
  const parts: string[] = [];
  for (const raw of asArray(content)) {
    const block = asRecord(raw);
    if (!block) continue;
    const kind = asString(block["type"])?.toLowerCase();
    if (kind === "text" || kind === "input_text" || kind === "output_text") {
      const text = asString(block["text"]);
      if (text) parts.push(text);
    }
  }
  return parts.join("").trim();
}

function basename(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1);
}
