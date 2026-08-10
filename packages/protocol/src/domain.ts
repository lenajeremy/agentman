import { z } from "zod";

/**
 * The agent CLIs we know how to observe. Adding a new one means writing a
 * source adapter in the daemon — nothing here or in the app should need to
 * special-case a specific kind beyond presentation (glyph, colour).
 */
export const AgentKind = z.enum(["claude", "codex", "opencode"]);
export type AgentKind = z.infer<typeof AgentKind>;

/**
 * `waiting_input` means the agent is blocked on a permission prompt and is
 * going nowhere until a human answers. It is the most actionable state, so the
 * app sorts it above everything else.
 */
export const SessionState = z.enum(["busy", "idle", "waiting_input", "ended"]);
export type SessionState = z.infer<typeof SessionState>;

/**
 * How (and whether) we can deliver a message into a running session. Surfaced
 * in the UI as a badge so the user always knows what to expect before sending:
 *
 * - `api`  — a real API on the agent itself. Instant, works mid-turn. (OpenCode)
 * - `tmux` — session runs under a tmux wrapper; we type into it. Works mid-turn.
 * - `hook` — no live channel; queued and delivered when the current turn ends.
 *            Best-effort: the Stop hook block can be discarded by the CLI.
 * - `none` — read-only. The composer is disabled.
 */
export const InjectMode = z.enum(["api", "tmux", "hook", "none"]);
export type InjectMode = z.infer<typeof InjectMode>;

export const AgentSession = z.object({
  /** Stable composite id: `${kind}:${nativeId}`. Unique across agent kinds. */
  id: z.string(),
  kind: AgentKind,
  /** The agent's own session identifier, as it appears on disk / in its API. */
  nativeId: z.string(),
  /** Human label. Claude supplies one; otherwise derived from the cwd basename. */
  name: z.string(),
  cwd: z.string(),
  state: SessionState,
  inject: InjectMode,
  startedAt: z.number(),
  lastActivityAt: z.number(),
});
export type AgentSession = z.infer<typeof AgentSession>;

export const ToolInfo = z.object({
  name: z.string(),
  /** One-line human summary, e.g. the command for Bash or the path for Read. */
  summary: z.string().optional(),
  status: z.enum(["running", "ok", "error"]).optional(),
});
export type ToolInfo = z.infer<typeof ToolInfo>;

export const AgentMessage = z.object({
  /**
   * Stable and idempotent across re-reads — this is what lets the app dedupe
   * when a live-tailed message also arrives in a history page. Derived from the
   * transcript's own uuid where one exists, else hashed from the byte offset.
   */
  id: z.string(),
  sessionId: z.string(),
  role: z.enum(["user", "assistant", "tool", "system"]),
  ts: z.number(),
  text: z.string().optional(),
  tool: ToolInfo.optional(),
  /** Subagent output. Collapsed behind a chip in the UI rather than inlined. */
  isSidechain: z.boolean().optional(),
});
export type AgentMessage = z.infer<typeof AgentMessage>;

/**
 * Opaque to the app, which only ever echoes it back. For file-backed agents it
 * encodes a byte offset; for OpenCode it wraps that API's own pagination. This
 * indirection is what lets three very different agents page through one call.
 */
export type Cursor = string;

export const MessagePage = z.object({
  sessionId: z.string(),
  /** Chronological (oldest first), regardless of the direction we read in. */
  messages: z.array(AgentMessage),
  /** Pass as `before` to fetch the next page further back. Absent at the start. */
  nextCursor: z.string().optional(),
  hasMore: z.boolean(),
});
export type MessagePage = z.infer<typeof MessagePage>;
