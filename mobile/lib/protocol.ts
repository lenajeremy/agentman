/**
 * The wire contract, mirroring internal/protocol in the Go daemon.
 *
 * Kept hand-written rather than generated: it is small, it changes rarely, and
 * a generator would be more machinery than the problem deserves. If these drift
 * from the Go types, the app shows empty screens — so the daemon's `am doctor`
 * is the backstop, not this file.
 */

export const PROTOCOL_VERSION = 2;

/**
 * Pairing codes are ten fully random digits. They are single-use and
 * short-lived; the relay rate-limits failed redemption attempts.
 */
export const PAIRING_CODE_LENGTH = 10;

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
  /** Stable daemon identity used to reject answers to a prompt that moved on. */
  id: string;
  prompt: string;
  title?: string;
  detail?: string;
  options: QuestionOption[];
  multiple?: boolean;
  custom?: boolean;
}

export interface QuestionAnswer {
  questionId?: string;
  optionKey?: string;
  optionKeys?: string[];
  answerText?: string;
}

export interface QuestionOption {
  /** What gets sent to choose this option. */
  key: string;
  label: string;
  /** Explanatory text shown beneath the label. */
  description?: string;
  /** Claude's side-by-side content panel for this option. */
  preview?: string;
  selected?: boolean;
  /** Whether Claude has this checkbox enabled in a multi-select form. */
  checked?: boolean;
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
  /** What the agent is actually running ("claude-opus-5", "gpt-5.6-sol").
   *  Absent until it has replied once — none of the CLIs record it before. */
  model?: string;
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
  /** Relay-assigned identity of the app connection that sent a request. */
  from?: string;
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
  | "answer_question"
  | "register_push";

export interface Request {
  type: RequestType;
  sessionId?: string;
  before?: string;
  limit?: number;
  text?: string;
  clientId?: string;
  /** Echoes the question snapshot the user actually answered. */
  questionId?: string;
  /** Expo push token on register_push. */
  pushToken?: string;
  /** Chooses an option on answer_question. */
  optionKey?: string;
  optionKeys?: string[];
  answerText?: string;
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
  | "app_disconnected"
  | "error";

export interface Control {
  type: ControlType;
  daemonOnline?: boolean;
  code?: string;
  expiresAt?: number;
  lastSeenAt?: number;
  deviceId?: string;
  message?: string;
}

/** Parse an untrusted websocket frame before application code touches it. */
export function decodeEnvelope(raw: string): Envelope | null {
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return null;
  }
  if (
    !isRecord(value) ||
    (!finiteNumber(value.v) || !Number.isInteger(value.v)) ||
    !boundedString(value.id, 256, true) ||
    !isOneOf(value.to, ["daemon", "app", "relay"] as const) ||
    !isRecord(value.payload) ||
    !optionalBoundedString(value.from, 512) ||
    !optionalBoundedString(value.replyTo, 256)
  ) {
    return null;
  }
  // A relay on another protocol version sends the error in the requester's
  // version. Accept only that narrow cross-version control shape so the app can
  // explain the mismatch instead of sitting on a permanently blank socket.
  if (value.v !== PROTOCOL_VERSION && !(
    value.to === "relay" && isRecord(value.payload) &&
    value.payload.type === "error" &&
    value.payload.message === "unsupported protocol version"
  )) return null;
  return value as unknown as Envelope;
}

/** Validate a relay-owned control payload. */
export function decodeControl(value: unknown): Control | null {
  if (!isRecord(value) || !isOneOf(value.type, [
    "hello", "pair_request", "pair_code", "daemon_online", "daemon_offline",
    "app_disconnected", "error",
  ] as const)) return null;
  if (
    (value.daemonOnline !== undefined && typeof value.daemonOnline !== "boolean") ||
    !optionalBoundedString(value.code, 256) ||
    !optionalFiniteNumber(value.expiresAt) ||
    !optionalFiniteNumber(value.lastSeenAt) ||
    !optionalBoundedString(value.deviceId, 512) ||
    !optionalBoundedString(value.message, 4096)
  ) return null;
  return value as unknown as Control;
}

/** Validate a daemon event, including its nested session/message data. */
export function decodeDaemonEvent(value: unknown): DaemonEvent | null {
  if (!isRecord(value) || !isOneOf(value.type, [
    "sessions", "session_update", "session_gone", "messages", "page",
    "turn_complete", "send_result", "error",
  ] as const)) return null;

  switch (value.type) {
    case "sessions":
      if (!boundedArray(value.sessions, 10_000, isSession)) return null;
      break;
    case "session_update":
      if (!isSession(value.session)) return null;
      break;
    case "session_gone":
      if (!boundedString(value.sessionId, 512, true)) return null;
      break;
    case "messages":
      if (!boundedString(value.sessionId, 512, true) ||
          !boundedArray(value.messages, 100, isMessage)) return null;
      break;
    case "page":
      if (!isPage(value.page)) return null;
      break;
    case "turn_complete":
      if (!boundedString(value.sessionId, 512, true) ||
          !optionalBoundedString(value.sessionName, 4096) ||
          !optionalBoundedString(value.preview, 64 * 1024)) return null;
      break;
    case "send_result":
      if (!boundedString(value.clientId, 256, true) ||
          !isOneOf(value.status, ["delivered", "queued", "failed"] as const) ||
          !optionalBoundedString(value.sessionId, 512) ||
          !optionalBoundedString(value.error, 64 * 1024)) return null;
      break;
    case "error":
      if (!boundedString(value.error, 64 * 1024, true) ||
          !optionalBoundedString(value.sessionId, 512)) return null;
      break;
  }
  return value as unknown as DaemonEvent;
}

function isSession(value: unknown): value is Session {
  if (!isRecord(value)) return false;
  return boundedString(value.id, 512, true) &&
    isOneOf(value.kind, ["claude", "codex", "opencode"] as const) &&
    boundedString(value.nativeId, 512, true) &&
    boundedString(value.name, 4096) &&
    boundedString(value.cwd, 64 * 1024) &&
    isOneOf(value.state, ["busy", "idle", "waiting_input", "ended"] as const) &&
    isOneOf(value.inject, ["api", "tmux", "hook", "none"] as const) &&
    finiteNumber(value.startedAt) && finiteNumber(value.lastActivityAt) &&
    optionalBoundedString(value.model, 4096) &&
    (value.question === undefined || isQuestion(value.question));
}

function isQuestion(value: unknown): value is Question {
  if (!isRecord(value) || !boundedString(value.prompt, 64 * 1024) ||
      !boundedString(value.id, 256, true) ||
      !optionalBoundedString(value.title, 4096) ||
      !optionalBoundedString(value.detail, 256 * 1024) ||
      (value.multiple !== undefined && typeof value.multiple !== "boolean") ||
      (value.custom !== undefined && typeof value.custom !== "boolean")) return false;
  return boundedArray(value.options, 256, (option): option is QuestionOption =>
    isRecord(option) && boundedString(option.key, 4096, true) &&
    boundedString(option.label, 64 * 1024) &&
    optionalBoundedString(option.description, 64 * 1024) &&
    optionalBoundedString(option.preview, 256 * 1024) &&
    (option.selected === undefined || typeof option.selected === "boolean") &&
    (option.checked === undefined || typeof option.checked === "boolean"));
}

function isMessage(value: unknown): value is Message {
  if (!isRecord(value) || !boundedString(value.id, 4096, true) ||
      !boundedString(value.sessionId, 512, true) ||
      !isOneOf(value.role, ["user", "assistant", "tool", "system"] as const) ||
      !finiteNumber(value.ts) || !optionalBoundedString(value.text, 512 * 1024) ||
      (value.isSidechain !== undefined && typeof value.isSidechain !== "boolean")) return false;
  if (value.tool === undefined) return true;
  return isRecord(value.tool) && boundedString(value.tool.name, 4096, true) &&
    optionalBoundedString(value.tool.summary, 64 * 1024) &&
    (value.tool.status === undefined ||
      isOneOf(value.tool.status, ["running", "ok", "error"] as const));
}

function isPage(value: unknown): value is Page {
  return isRecord(value) && boundedString(value.sessionId, 512, true) &&
    boundedArray(value.messages, 100, isMessage) &&
    optionalBoundedString(value.nextCursor, 4096) && typeof value.hasMore === "boolean";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function finiteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function optionalFiniteNumber(value: unknown): boolean {
  return value === undefined || finiteNumber(value);
}

function boundedString(value: unknown, max: number, required = false): value is string {
  return typeof value === "string" && value.length <= max && (!required || value.length > 0);
}

function optionalBoundedString(value: unknown, max: number): boolean {
  return value === undefined || boundedString(value, max);
}

function boundedArray<T>(
  value: unknown,
  max: number,
  predicate: (item: unknown) => item is T,
): value is T[] {
  return Array.isArray(value) && value.length <= max && value.every(predicate);
}

function isOneOf<T extends string>(value: unknown, options: readonly T[]): value is T {
  return typeof value === "string" && options.includes(value as T);
}
