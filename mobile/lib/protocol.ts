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

/**
 * The agents the daemon knows today. Typed open-ended on purpose: a newer
 * daemon adding an agent (Gemini, Grok) must not make this app reject the
 * whole session list — the unknown agent shows with a generic icon instead.
 */
export type AgentKind =
  | "claude"
  | "codex"
  | "opencode"
  | "cursor"
  | "cursor-cli"
  | "kiro"
  | "antigravity"
  | (string & {});
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
  /** Choosing this may carry a short note — "No, and do this instead". The
   *  answer is optionKey plus answerText; no text is the plain choice. */
  withText?: boolean;
}

export type ModelScope = "session" | "default";

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
  /** The agent's own name for its mode ("plan", "accept-edits"). Absent
   *  means its default. Switchable to one of `modes` when the agent offers any. */
  mode?: string;
  /** How full the model's context window is, in whole percent. */
  contextPercent?: number;
  /** Documents the agent wrote for the user, and how many await review. */
  artifacts?: number;
  artifactsToReview?: number;
  /** Modes the phone may switch to with set_mode, in the CLI's own names. */
  modes?: string[];
  /** Model ids the phone may switch to with set_model. */
  models?: string[];
  /** What a model switch touches: this session only, or also the CLI's
   *  default for new sessions. Absent means models are not switched here. */
  modelScope?: ModelScope;
  /** Present only while the agent is waiting on a decision. */
  question?: Question;
  /** Web servers the agent has started. Absent from daemons that predate it. */
  servers?: Server[];
  /** Messages sent while the agent was working that it has not read yet,
   *  oldest first. The agent holds them until its current step ends; each
   *  leaves this list when it is read, and arrives as an ordinary message.
   *  Absent from daemons that predate it, and for agents without a queue. */
  queued?: string[];
}

/** A local web server an agent started, which the phone can open. */
export interface Server {
  port: number;
  /** The listening program, e.g. "node". */
  command?: string;
  /** The page's <title>, when it serves HTML. */
  title?: string;
  /** The public preview link while the server is being shared. */
  link?: string;
}

/**
 * A document an agent wrote for the user apart from the conversation: a plan,
 * a task list, a walkthrough, a screenshot.
 */
export interface Artifact {
  /** A plain file name, never a path; what read_artifact takes as `path`. */
  name: string;
  /** "plan" | "task" | "walkthrough" | "spec" | "image" | "video" | "file",
   *  open-ended so a newer agent's kind still lists. */
  kind: string;
  title?: string;
  summary?: string;
  updatedAt: number;
  size: number;
  mime?: string;
  /** The agent asked for approval and has had no answer since it changed. */
  review?: boolean;
}

export interface Tool {
  name: string;
  summary?: string;
  status?: "running" | "ok" | "error";
  /** What the step did in plain words ("Ran the Go tests"); summary is the
   *  raw command or path behind it. Absent from an older Mac. */
  title?: string;
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
  | "send_queued"
  | "answer_question"
  | "register_push"
  | "open_server"
  | "close_server"
  | "stop_server"
  | "list_files"
  | "read_file"
  | "list_changes"
  | "file_diff"
  | "read_seen_file"
  | "tools_title"
  | "read_file_chunk"
  | "read_seen_file_chunk"
  | "list_directories"
  | "start_session"
  | "list_folders"
  | "directory_sessions"
  | "resume_session"
  | "end_session"
  | "create_directory"
  | "list_artifacts"
  | "read_artifact"
  | "review_artifact"
  | "set_mode"
  | "set_model"
  | "list_commits"
  | "commit_files"
  | "commit_diff"
  | "list_branches";

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
  /** Which alerts this phone wants pushed, on register_push. */
  notify?: { finished: boolean; needsYou: boolean };
  /** Chooses an option on answer_question. */
  optionKey?: string;
  optionKeys?: string[];
  answerText?: string;
  /** The server on open_server and close_server. */
  port?: number;
  /** Relative to session cwd for workspace reads, or Mac home for launches. */
  path?: string;
  /** Where a chunk request starts reading, in bytes. */
  offset?: number;
  /** Tickets for images already left with the relay, on send_message. */
  uploadIds?: string[];
  /** The tool calls a tools_title request is about. */
  steps?: Tool[];
  /** Agent to launch; path is relative to the Mac user's home directory. */
  kind?: AgentKind;
  /** The verdict on review_artifact; `text` carries the comment. */
  approve?: boolean;
  /** A full commit hash, as list_commits reported it. */
  commit?: string;
  /** The branch list_commits reads, in full, as list_branches reported it.
   *  Absent reads whatever is checked out. */
  ref?: string;
}

export type EventType =
  | "sessions"
  | "session_update"
  | "session_gone"
  | "messages"
  | "page"
  | "tools_title"
  | "turn_complete"
  | "send_result"
  | "server_opened"
  | "server_stopped"
  | "error"
  | "workspace"
  | "directories"
  | "session_started"
  | "folders"
  | "directory_sessions"
  | "session_ended"
  | "artifacts";

export interface WorkspaceEntry { name: string; directory: boolean; size?: number }
export interface WorkspaceChange {
  path: string;
  status: string;
  /** Lines added and removed. Absent or zero for a binary file. */
  added?: number;
  removed?: number;
}
/**
 * The file an image preview was made from. A preview too large for a phone or
 * the relay is resized on the Mac first, and nothing in its bytes says so;
 * this does, along with what fetching the file itself would bring.
 */
export interface ImageSource {
  size: number;
  mime: string;
  /** Absent for a format the Mac cannot decode. */
  width?: number;
  height?: number;
  /** True when the preview is a smaller copy rather than the file itself. */
  reduced?: boolean;
}

/**
 * One commit, as far as it concerns the session's directory. Files, added and
 * removed count only what changed there, and a merge is measured against its
 * first parent. All three are absent when the Mac could not count in time.
 */
export interface WorkspaceCommit {
  hash: string;
  subject: string;
  /** The rest of the message, on kind "commit" only. */
  body?: string;
  author: string;
  /** Epoch milliseconds. */
  authoredAt: number;
  merge?: boolean;
  files?: number;
  added?: number;
  removed?: number;
}

/** A branch whose history can be shown. Showing it checks nothing out. */
export interface WorkspaceBranch {
  /** "main", or "origin/main" for a remote one. */
  name: string;
  /** The full name to send back: refs/heads/… or refs/remotes/…. */
  ref: string;
  remote?: boolean;
  /** The branch checked out on the Mac. */
  current?: boolean;
  /** When its newest commit was made, epoch milliseconds. */
  updatedAt: number;
}

export interface WorkspaceResult {
  kind:
    | "directory" | "file" | "changes" | "diff" | "chunk" | "artifact"
    | "commits" | "commit" | "branches";
  sessionId: string;
  path?: string;
  entries?: WorkspaceEntry[];
  changes?: WorkspaceChange[];
  text?: string;
  image?: string;
  mime?: string;
  diff?: string;
  truncated?: boolean;
  /** How many entries the daemon withheld as private. */
  hidden?: number;
  /** Set alongside `image`. Absent from a Mac too old to describe it. */
  source?: ImageSource;
  /** One piece of a file, base64, on kind "chunk": where it starts, how long
   *  the whole file is, and a stamp that changes when the file does. */
  data?: string;
  offset?: number;
  size?: number;
  version?: string;
  /** One page of history on kind "commits", newest first. `truncated` means
   *  older commits follow. */
  commits?: WorkspaceCommit[];
  /** The checked-out branch, absent when HEAD is detached. */
  branch?: string;
  /** The commit on kind "commit", whose files are in `changes`. */
  commit?: WorkspaceCommit;
  /** The answer on kind "branches", newest first. */
  branches?: WorkspaceBranch[];
}

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
  /** Set on server_opened. */
  port?: number;
  link?: string;
  error?: string;
  workspace?: WorkspaceResult;
  directories?: string[];
  path?: string;
  /** Agent counts. The whole answer on "folders"; alongside `directories` on
   *  a browse listing, one entry per child that has agents under it. */
  folders?: Folder[];
  /** The answer on "artifacts"; [] when the session has none. */
  artifacts?: Artifact[];
  /** Rides on "sessions": which agentman the Mac runs, and what is newer. */
  daemon?: DaemonInfo;
}

/** The agentman running on the Mac. */
export interface DaemonInfo {
  version: string;
  /** The newest release. Absent until the Mac has checked, or when its
   *  checks are switched off. */
  latest?: string;
  /** How many releases are newer than `version`. */
  behind?: number;
  /** Those releases, newest first, with what each changed. */
  releases?: ReleaseNote[];
  /** The command that upgrades this install: Homebrew, npm or the script. */
  upgrade?: string;
  /** Where every release's notes can be read. */
  changelog?: string;
}

export interface ReleaseNote {
  version: string;
  /** Milliseconds. */
  date?: number;
  changes?: string[];
  url?: string;
}

/** One directory agents have run in.
 *
 *  Counts cover the whole subtree, because selecting a folder does too: a
 *  repository's bin/ is the same project as its root. */
export interface Folder {
  /** Absolute on "folders"; the child's own name on a browse listing. */
  path: string;
  /** Every session ever recorded here, running or long gone. */
  agents: number;
  /** How many are alive now. */
  running?: number;
  lastActivityAt?: number;
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
    "turn_complete", "send_result", "server_opened", "server_stopped", "error", "workspace",
    "directories", "session_started", "folders", "directory_sessions",
    "session_ended", "artifacts", "tools_title",
  ] as const)) return null;

  switch (value.type) {
    case "sessions":
      // An older Mac leaves an empty list out entirely. That is an empty
      // board, not a malformed frame: dropping it kept ended sessions on screen.
      value.sessions ??= [];
      // One session the app cannot read is left out, not the whole list. A
      // single session with no native id used to reject the frame, and with
      // it every agent that was not sending live updates: the idle ones.
      if (!Array.isArray(value.sessions) || value.sessions.length > 10_000) return null;
      value.sessions = value.sessions.filter(isSession);
      // Extra news about the Mac, never a reason to lose the board.
      if (value.daemon !== undefined && !isDaemonInfo(value.daemon)) delete value.daemon;
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
    case "tools_title":
      if (!optionalBoundedString(value.preview, 4096)) return null;
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
    case "server_opened":
      if (!boundedString(value.sessionId, 512, true) || !isPort(value.port) ||
          !isLink(value.link)) return null;
      break;
    case "server_stopped":
      if (!boundedString(value.sessionId, 512, true) || !isPort(value.port)) return null;
      break;
    case "error":
      if (!boundedString(value.error, 64 * 1024, true) ||
          !optionalBoundedString(value.sessionId, 512) ||
          (value.port !== undefined && !isPort(value.port))) return null;
      break;
    case "workspace":
      if (!isWorkspaceResult(value.workspace)) return null;
      break;
    case "directories":
      if (!optionalBoundedString(value.path, 4096) ||
          (value.directories !== undefined &&
            !boundedArray(value.directories, 200, (name): name is string =>
              boundedString(name, 256, true))) ||
          !optionalFolders(value.folders)) return null;
      break;
    case "folders":
      if (!optionalFolders(value.folders)) return null;
      break;
    case "directory_sessions":
      value.sessions ??= [];
      if (!Array.isArray(value.sessions) || value.sessions.length > 10_000 ||
          !optionalBoundedString(value.path, 4096)) return null;
      value.sessions = value.sessions.filter(isSession);
      break;
    case "session_started":
    case "session_ended":
      if (!boundedString(value.sessionId, 512, true)) return null;
      break;
    case "artifacts":
      // A missing list is an empty one: the field is new, and [] and absent
      // say the same thing to a screen that lists them.
      if (!boundedString(value.sessionId, 512, true) ||
          (value.artifacts !== undefined && !boundedArray(value.artifacts, MAX_ARTIFACTS, isArtifact))) {
        return null;
      }
      break;
  }
  return value as unknown as DaemonEvent;
}

/** More than the daemon ever sends, so a cap there is never a refusal here. */
const MAX_ARTIFACTS = 500;

/**
 * An artifact name is echoed back to the Mac as `path`, so it has to be a
 * plain name: anything with a separator or a parent reference is refused here
 * rather than trusted to the daemon's own check.
 */
export function isArtifactName(value: unknown): value is string {
  return boundedString(value, 255, true) &&
    !value.includes("/") && !value.includes("\\") && !value.includes("..") &&
    !/[\u0000-\u001f\u007f]/.test(value);
}

function isArtifact(value: unknown): value is Artifact {
  return isRecord(value) &&
    isArtifactName(value.name) &&
    boundedString(value.kind, 64, true) &&
    optionalBoundedString(value.title, 2048) &&
    optionalBoundedString(value.summary, 16 * 1024) &&
    finiteNumber(value.updatedAt) && value.updatedAt >= 0 &&
    finiteNumber(value.size) && value.size >= 0 &&
    optionalBoundedString(value.mime, 128) &&
    (value.review === undefined || typeof value.review === "boolean");
}

function optionalFolders(value: unknown): boolean {
  return value === undefined || boundedArray(value, 500, isFolder);
}

export function isDaemonInfo(value: unknown): value is DaemonInfo {
  return isRecord(value) &&
    boundedString(value.version, 64, true) &&
    optionalBoundedString(value.latest, 64) &&
    optionalFiniteNumber(value.behind) &&
    (value.releases === undefined || boundedArray(value.releases, 20, isReleaseNote)) &&
    optionalBoundedString(value.upgrade, 512) &&
    optionalBoundedString(value.changelog, 2048);
}

function isReleaseNote(value: unknown): value is ReleaseNote {
  return isRecord(value) &&
    boundedString(value.version, 64, true) &&
    optionalFiniteNumber(value.date) &&
    (value.changes === undefined ||
      boundedArray(value.changes, 50, (change): change is string => boundedString(change, 1000))) &&
    optionalBoundedString(value.url, 2048);
}

function isFolder(value: unknown): value is Folder {
  return isRecord(value) &&
    boundedString(value.path, 4096, true) &&
    optionalFiniteNumber(value.agents) && typeof value.agents === "number" &&
    optionalFiniteNumber(value.running) &&
    optionalFiniteNumber(value.lastActivityAt);
}

const IMAGE_TYPES = ["image/png", "image/jpeg", "image/gif", "image/webp"] as const;
/** One megabyte of file is 1.4 MB of base64; this leaves room and no more. */
const MAX_CHUNK_BASE64 = 2 * 1024 * 1024;
/** Nothing over 32 MiB is previewed, so nothing over it can be downloaded. */
const MAX_ORIGINAL_BYTES = 32 * 1024 * 1024;

function isByteCount(value: unknown, max: number): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= 0 && value <= max;
}

function isImageSource(value: unknown): value is ImageSource {
  return isRecord(value) &&
    isByteCount(value.size, MAX_ORIGINAL_BYTES) &&
    isOneOf(value.mime, IMAGE_TYPES) &&
    (value.width === undefined || isByteCount(value.width, 1_000_000)) &&
    (value.height === undefined || isByteCount(value.height, 1_000_000)) &&
    (value.reduced === undefined || typeof value.reduced === "boolean");
}

function isWorkspaceResult(value: unknown): value is WorkspaceResult {
  if (!isRecord(value) ||
      !isOneOf(value.kind, ["directory", "file", "changes", "diff", "chunk", "artifact", "commits", "commit", "branches"] as const) ||
      !boundedString(value.sessionId, 512, true) || !optionalBoundedString(value.path, 4096) ||
      !optionalBoundedString(value.text, 256 * 1024) ||
      !optionalBoundedString(value.image, 3 * 1024 * 1024) ||
      !optionalBoundedString(value.diff, 256 * 1024) ||
      !optionalBoundedString(value.mime, 128) ||
      (value.truncated !== undefined && typeof value.truncated !== "boolean") ||
      !optionalFiniteNumber(value.hidden)) return false;
  if (value.entries !== undefined && !boundedArray(value.entries, 500, (entry): entry is WorkspaceEntry =>
      isRecord(entry) && boundedString(entry.name, 4096, true) &&
      typeof entry.directory === "boolean" && optionalFiniteNumber(entry.size))) return false;
  if (value.changes !== undefined && !boundedArray(value.changes, 500, (change): change is WorkspaceChange =>
      isRecord(change) && boundedString(change.path, 4096, true) && boundedString(change.status, 2, true) &&
      optionalFiniteNumber(change.added) && optionalFiniteNumber(change.removed))) return false;
  if (value.image !== undefined && (
      typeof value.image !== "string" ||
      !isOneOf(value.mime, IMAGE_TYPES) ||
      !/^[A-Za-z0-9+/]*={0,2}$/.test(value.image)
  )) return false;
  if (value.source !== undefined && !isImageSource(value.source)) return false;
  if (value.commits !== undefined && !boundedArray(value.commits, 200, isCommit)) return false;
  if (value.commit !== undefined && !isCommit(value.commit)) return false;
  if (!optionalBoundedString(value.branch, 512)) return false;
  if (value.branches !== undefined && !boundedArray(value.branches, 300, isBranch)) return false;
  // A piece is written straight to a file, so everything about it is checked
  // here: nothing empty, nothing oversized, and nothing that is not base64.
  if (value.kind === "chunk") {
    return boundedString(value.data, MAX_CHUNK_BASE64, true) &&
      /^[A-Za-z0-9+/]*={0,2}$/.test(value.data) &&
      isOneOf(value.mime, IMAGE_TYPES) &&
      isByteCount(value.offset ?? 0, MAX_ORIGINAL_BYTES) &&
      isByteCount(value.size, MAX_ORIGINAL_BYTES) && value.size > 0 &&
      boundedString(value.version, 64, true);
  }
  return value.data === undefined;
}

const COMMIT_HASH = /^[0-9a-f]{40}(?:[0-9a-f]{24})?$/;

/** The hash is the one thing here that goes back to the Mac, in the next
 *  request, so it is held to exactly the shape the daemon accepts. */
function isCommit(value: unknown): value is WorkspaceCommit {
  return isRecord(value) &&
    typeof value.hash === "string" && COMMIT_HASH.test(value.hash) &&
    boundedString(value.subject, 4096) &&
    optionalBoundedString(value.body, 64 * 1024) &&
    boundedString(value.author, 1024) &&
    finiteNumber(value.authoredAt) &&
    (value.merge === undefined || typeof value.merge === "boolean") &&
    (value.files === undefined || isCount(value.files)) &&
    (value.added === undefined || isLineCount(value.added)) &&
    (value.removed === undefined || isLineCount(value.removed));
}

/** The ref goes back to the Mac in the next request, so it is held to the
 *  shape the daemon accepts: a plain local or remote branch name. */
const BRANCH_REF = /^refs\/(heads|remotes)\/(?!.*(\.\.|@\{|\/\/))[^\s~^:?*[\\\x00-\x1f\x7f]+$/;

function isBranch(value: unknown): value is WorkspaceBranch {
  return isRecord(value) &&
    boundedString(value.name, 512, true) &&
    typeof value.ref === "string" && value.ref.length <= 512 && BRANCH_REF.test(value.ref) &&
    (value.remote === undefined || typeof value.remote === "boolean") &&
    (value.current === undefined || typeof value.current === "boolean") &&
    finiteNumber(value.updatedAt);
}

/** A commit can touch more lines than isCount allows; a vendored dependency
 *  or a generated lockfile easily passes a hundred thousand. */
function isLineCount(value: unknown): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= 0 && value <= 1e9;
}

function isSession(value: unknown): value is Session {
  if (!isRecord(value)) return false;
  return boundedString(value.id, 512, true) &&
    boundedString(value.kind, 64, true) &&
    boundedString(value.nativeId, 512, true) &&
    boundedString(value.name, 4096) &&
    boundedString(value.cwd, 64 * 1024) &&
    isOneOf(value.state, ["busy", "idle", "waiting_input", "ended"] as const) &&
    isOneOf(value.inject, ["api", "tmux", "hook", "none"] as const) &&
    finiteNumber(value.startedAt) && finiteNumber(value.lastActivityAt) &&
    optionalBoundedString(value.model, 4096) &&
    optionalBoundedString(value.mode, 256) &&
    (value.contextPercent === undefined || isPercent(value.contextPercent)) &&
    (value.artifacts === undefined || isCount(value.artifacts)) &&
    (value.artifactsToReview === undefined || isCount(value.artifactsToReview)) &&
    (value.modes === undefined || boundedArray(value.modes, MAX_MODES, isModeName)) &&
    (value.models === undefined || boundedArray(value.models, MAX_MODELS, isModelName)) &&
    (value.modelScope === undefined || isOneOf(value.modelScope, ["session", "default"] as const)) &&
    (value.question === undefined || isQuestion(value.question)) &&
    (value.servers === undefined || boundedArray(value.servers, 64, isServer)) &&
    (value.queued === undefined ||
      boundedArray(value.queued, MAX_QUEUED, (text): text is string => boundedString(text, 64 * 1024, true)));
}

/** More waiting messages than anyone types ahead of an agent; past it the
 *  session is refused rather than drawn. */
const MAX_QUEUED = 50;

function isServer(value: unknown): value is Server {
  return isRecord(value) && isPort(value.port) &&
    optionalBoundedString(value.command, 256) &&
    optionalBoundedString(value.title, 1024) &&
    (value.link === undefined || isLink(value.link));
}

/** As many as the daemon sends; it drops any entry these would refuse. */
const MAX_MODES = 16;
const MAX_MODELS = 64;

function isModeName(value: unknown): value is string {
  return boundedString(value, 64, true) && isSwitchValue(value);
}

function isModelName(value: unknown): value is string {
  return boundedString(value, 128, true) && isSwitchValue(value);
}

/** A name that goes back to the Mac exactly as it came: one line, no ends of
 *  whitespace, nothing a terminal would read. */
function isSwitchValue(value: string): boolean {
  return value.trim() === value && !/[\u0000-\u001f\u007f-\u009f]/.test(value);
}

function isPercent(value: unknown): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= 0 && value <= 100;
}

function isCount(value: unknown): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= 0 && value <= 100_000;
}

function isPort(value: unknown): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= 1 && value <= 65535;
}

/** A preview link the app will open in a browser. Anything but http(s) — a
 *  javascript: or custom-scheme URL from a compromised relay — is refused. */
function isLink(value: unknown): value is string {
  return boundedString(value, 2048, true) && /^https?:\/\/[^\s]+$/.test(value);
}

function isQuestion(value: unknown): value is Question {
  if (!isRecord(value) || !boundedString(value.prompt, 64 * 1024) ||
      !boundedString(value.id, 256, true) ||
      !optionalBoundedString(value.title, 4096) ||
      !optionalBoundedString(value.detail, 256 * 1024) ||
      (value.multiple !== undefined && typeof value.multiple !== "boolean") ||
      (value.custom !== undefined && typeof value.custom !== "boolean")) return false;
  // An older Mac sends null for options it withdrew because they could not be
  // answered safely from here. The question is still worth showing, and
  // rejecting it took the whole session list it arrived in.
  value.options ??= [];
  return boundedArray(value.options, 256, (option): option is QuestionOption =>
    isRecord(option) && boundedString(option.key, 4096, true) &&
    (option.withText === undefined || typeof option.withText === "boolean") &&
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
  // A title the app cannot use is dropped, not the message with it.
  if (isRecord(value.tool) && value.tool.title !== undefined && !optionalBoundedString(value.tool.title, 4096)) {
    delete value.tool.title;
  }
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
