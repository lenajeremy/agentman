/**
 * The wire contract, mirroring internal/protocol in the Go daemon.
 *
 * Kept hand-written rather than generated: it is small, it changes rarely, and
 * a generator would be more machinery than the problem deserves. If these drift
 * from the Go types, the app shows empty screens — so the daemon's `am doctor`
 * is the backstop, not this file.
 */

export const PROTOCOL_VERSION = 1;

/**
 * Pairing codes are eight digits: a two-digit account shard followed by six
 * random ones. The shard lets the relay charge a failed guess to the group of
 * accounts it was aimed at, so one person being flooded cannot stop everyone
 * else from pairing.
 */
export const PAIRING_CODE_LENGTH = 8;

export type AgentKind = "claude" | "codex" | "opencode";
export type SessionState = "busy" | "idle" | "waiting_input" | "ended";

/**
 * How a message can reach a session. Surfaced in the UI because these are not
 * equally good: `tmux` and `api` land immediately, `hook` only lands when the
 * agent's current turn ends and can be discarded by the CLI.
 */
export type InjectMode = "api" | "tmux" | "hook" | "none";

/** A decision an agent is blocked on, with the choices it is offering. */
export interface Question {
  prompt: string;
  title?: string;
  detail?: string;
  options: QuestionOption[];
}

export interface QuestionOption {
  /** What gets sent to choose this option. */
  key: string;
  label: string;
  selected?: boolean;
}

export interface Session {
  id: string;
  kind: AgentKind;
  nativeId: string;
  name: string;
  cwd: string;
  state: SessionState;
  inject: InjectMode;
  startedAt: number;
  lastActivityAt: number;
  /** Present only while the agent is waiting on a decision. */
  question?: Question;
}

export interface Tool {
  name: string;
  summary?: string;
  status?: "running" | "ok" | "error";
}

export interface Message {
  id: string;
  sessionId: string;
  role: "user" | "assistant" | "tool" | "system";
  ts: number;
  text?: string;
  tool?: Tool;
  isSidechain?: boolean;
}

export interface Page {
  sessionId: string;
  messages: Message[];
  nextCursor?: string;
  hasMore: boolean;
}

export type Peer = "daemon" | "app" | "relay";

export interface Envelope {
  v: number;
  id: string;
  replyTo?: string;
  to: Peer;
  payload: unknown;
}

export type RequestType =
  | "list_sessions"
  | "subscribe"
  | "unsubscribe"
  | "fetch_messages"
  | "send_message"
  | "interrupt"
  | "answer_question";

export interface Request {
  type: RequestType;
  sessionId?: string;
  before?: string;
  limit?: number;
  text?: string;
  clientId?: string;
  /** Chooses an option on answer_question. */
  optionKey?: string;
}

export type EventType =
  | "sessions"
  | "session_update"
  | "session_gone"
  | "messages"
  | "page"
  | "turn_complete"
  | "send_result"
  | "error";

export type SendStatus = "delivered" | "queued" | "failed";

export interface DaemonEvent {
  type: EventType;
  sessionId?: string;
  sessions?: Session[];
  session?: Session;
  messages?: Message[];
  page?: Page;
  sessionName?: string;
  preview?: string;
  clientId?: string;
  status?: SendStatus;
  error?: string;
}

export type ControlType =
  | "hello"
  | "pair_request"
  | "pair_code"
  | "daemon_online"
  | "daemon_offline"
  | "error";

export interface Control {
  type: ControlType;
  daemonOnline?: boolean;
  code?: string;
  expiresAt?: number;
  lastSeenAt?: number;
  message?: string;
}

let counter = 0;
export function newFrameId(): string {
  counter += 1;
  return `${Date.now().toString(36)}-${counter}`;
}
