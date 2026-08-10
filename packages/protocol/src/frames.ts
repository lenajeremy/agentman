import { z } from "zod";
import { AgentMessage, AgentSession, MessagePage } from "./domain.ts";

export const PROTOCOL_VERSION = 1;

/* ------------------------------------------------------------------ *
 * App → daemon (requests)
 * ------------------------------------------------------------------ */

/** Cheap and idempotent; the daemon answers from memory. */
const ListSessions = z.object({ type: z.literal("list_sessions") });

/**
 * Start/stop live message streaming for one session. The app subscribes when a
 * session screen gains focus and unsubscribes when it loses it, so an idle
 * session costs zero bandwidth — the reason we can stream everything live
 * without a server-side cache.
 */
const Subscribe = z.object({ type: z.literal("subscribe"), sessionId: z.string() });
const Unsubscribe = z.object({ type: z.literal("unsubscribe"), sessionId: z.string() });

/** Scrollback. `before` is a cursor from a previous page; omit for the newest. */
const FetchMessages = z.object({
  type: z.literal("fetch_messages"),
  sessionId: z.string(),
  before: z.string().optional(),
  limit: z.number().int().min(1).max(200).default(50),
});

const SendMessage = z.object({
  type: z.literal("send_message"),
  sessionId: z.string(),
  text: z.string().min(1),
  /** Echoed back on `send_result` so the app can settle its optimistic bubble. */
  clientId: z.string(),
});

/** Ctrl-C equivalent. Only meaningful for `tmux` and `api` sessions. */
const Interrupt = z.object({ type: z.literal("interrupt"), sessionId: z.string() });

export const AppRequest = z.discriminatedUnion("type", [
  ListSessions,
  Subscribe,
  Unsubscribe,
  FetchMessages,
  SendMessage,
  Interrupt,
]);
export type AppRequest = z.infer<typeof AppRequest>;

/* ------------------------------------------------------------------ *
 * Daemon → app (events and replies)
 * ------------------------------------------------------------------ */

const Sessions = z.object({ type: z.literal("sessions"), sessions: z.array(AgentSession) });

const SessionUpdate = z.object({
  type: z.literal("session_update"),
  session: AgentSession,
});

const SessionGone = z.object({ type: z.literal("session_gone"), sessionId: z.string() });

/** Live tail output for a subscribed session. */
const Messages = z.object({
  type: z.literal("messages"),
  sessionId: z.string(),
  messages: z.array(AgentMessage),
});

const Page = z.object({ type: z.literal("page"), page: MessagePage });

/**
 * The signal the whole notification feature hangs on: an agent finished a turn.
 * Carries enough text to make a useful notification body without the app having
 * to fetch anything.
 */
const TurnComplete = z.object({
  type: z.literal("turn_complete"),
  sessionId: z.string(),
  sessionName: z.string(),
  /** Tail of the agent's final message, already truncated for a notification. */
  preview: z.string().optional(),
});

const SendResult = z.object({
  type: z.literal("send_result"),
  clientId: z.string(),
  /**
   * `queued` is not a failure but is not delivery either — it means a `hook`
   * session will receive the text when its current turn ends. The UI must show
   * this distinctly rather than as a sent message.
   */
  status: z.enum(["delivered", "queued", "failed"]),
  error: z.string().optional(),
});

const DaemonError = z.object({
  type: z.literal("error"),
  message: z.string(),
  code: z.string().optional(),
});

export const DaemonEvent = z.discriminatedUnion("type", [
  Sessions,
  SessionUpdate,
  SessionGone,
  Messages,
  Page,
  TurnComplete,
  SendResult,
  DaemonError,
]);
export type DaemonEvent = z.infer<typeof DaemonEvent>;

/* ------------------------------------------------------------------ *
 * Envelope
 * ------------------------------------------------------------------ */

/**
 * The relay routes on the envelope and never inspects `payload`. Keeping the
 * body as a single field from day one is what makes optional end-to-end
 * encryption a drop-in later: seal `payload`, and a relay operator can route
 * traffic it provably cannot read.
 */
export const Envelope = z.object({
  v: z.literal(PROTOCOL_VERSION),
  /** Unique per frame; a reply echoes the request's id in `replyTo`. */
  id: z.string(),
  replyTo: z.string().optional(),
  to: z.enum(["daemon", "app"]),
  payload: z.unknown(),
});
export type Envelope = z.infer<typeof Envelope>;

/** Relay-generated control frames. These are the only frames it originates. */
export const RelayNotice = z.discriminatedUnion("type", [
  z.object({ type: z.literal("hello_ack"), daemonOnline: z.boolean() }),
  z.object({ type: z.literal("daemon_online") }),
  /** Sent immediately rather than buffering — the relay stores nothing. */
  z.object({ type: z.literal("daemon_offline"), lastSeenAt: z.number().optional() }),
  z.object({ type: z.literal("error"), message: z.string(), code: z.string().optional() }),
]);
export type RelayNotice = z.infer<typeof RelayNotice>;
