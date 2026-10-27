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
  /** You asked to hear from this agent itself, even while Agentman speaks
   *  for the agents. */
  alerts?: boolean;
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
  /** The feed rows of those messages, for an agent that writes a message to
   *  its history as soon as it is sent and reads it later (OpenCode): shown as
   *  waiting rather than as read. */
  queuedMessageIds?: string[];
  /** The Mac can press the agent's key that sends its waiting messages now.
   *  Absent from daemons before 0.18, which offered Send now for Claude only. */
  canSendQueued?: boolean;
  /** The conversation this one took over in the same terminal: Claude's
   *  /clear starts a new one there, and /resume switches to another. A thread
   *  showing that one follows to this. */
  previous?: string;
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
  /** Pictures attached to a user's message, as paths on the Mac: from how
   *  the agent recorded it, never from its words. */
  images?: string[];
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
  | "set_agent_alerts"
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
  | "list_branches"
  | "manager_send"
  | "manager_history"
  | "manager_stop"
  | "manager_reset"
  | "manager_image"
  | "manager_briefs"
  | "manager_card_reply"
  | "manager_card_dismiss"
  | "manager_entry_close"
  | "manager_schedules"
  | "manager_schedule_cancel"
  | "manager_settings"
  | "manager_set_settings"
  | "manager_check_permission"
  | "knowledge_list"
  | "knowledge_read"
  | "knowledge_write"
  | "knowledge_delete"
  | "keys_list"
  | "key_set"
  | "key_delete"
  | "transcribe"
  | "voice_account"
  | "list_commands"
  | "command_panel"
  | "find_files"
  | "attach_file";

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
  notify?: { finished: boolean; needsYou: boolean; agentman: boolean; agents: boolean };
  /** An agent's own alerts on or off, on set_agent_alerts. */
  alerts?: boolean;
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
  /** Agent to launch, where path is relative to the Mac user's home; or a
   *  note's tier on knowledge_read, or a request's kind on
   *  manager_check_permission. */
  kind?: AgentKind | (string & {});
  /** The verdict on review_artifact; `text` carries the comment. */
  approve?: boolean;
  /** A full commit hash, as list_commits reported it. */
  commit?: string;
  /** The branch list_commits reads, in full, as list_branches reported it.
   *  Absent reads whatever is checked out. */
  ref?: string;
  /** The card a manager_card_reply or manager_card_dismiss is about. */
  cardId?: string;
  /** The conversation entry a manager_entry_close takes out. */
  entryId?: string;
  /** A suggested note as the user edited it, on manager_card_reply. */
  note?: Note;
  /** The reminder or watch manager_schedule_cancel cancels. */
  scheduleId?: string;
  /** Agentman's settings, whole, on manager_set_settings. */
  manager?: ManagerSettings;
  /** A key's details on key_set; its value is never sent from the phone. */
  key?: Key;
  /** One part of a recording for the Mac to write out, on transcribe. */
  voice?: VoiceClip;
  /** One part of a file sent to an agent, on attach_file. */
  file?: FilePart;
}

/**
 * A voice prompt travels in parts: 16 kHz mono WAV, as Celery takes it, in
 * slices of its base64 small enough for the relay, joined on the Mac.
 */
export interface VoiceClip {
  id: string;
  part: number;
  parts: number;
  mime: string;
  data: string;
}

/** A part received, or, for the last, what the recording said. */
export interface Transcript {
  id: string;
  done: boolean;
  text?: string;
}

/** Whether the Mac is signed in to Celery, which writes voice prompts out. */
export interface VoiceAccount {
  signedIn: boolean;
  email?: string;
  plan?: string;
}

/**
 * One entry of an agent's "/" menu, as the agent itself would offer it: its
 * own commands and the user's skills. Sent like any message, except a panel
 * command sent bare, which the Mac reads off the terminal (command_panel).
 */
export interface SlashCommand {
  /** What follows the "/": "compact", "vercel:deploy". */
  name: string;
  /** What goes before the name when it is typed, "/" when absent. Codex's
   *  skills take "$". */
  prefix?: string;
  description?: string;
  /** "[instructions]", "<model>": what it takes. */
  argumentHint?: string;
  /** builtin, bundled, personal, project, plugin or managed. */
  source?: string;
  /** Sent without arguments, it answers in a panel: run it with command_panel. */
  panel?: boolean;
}

/** One part of a file sent to an agent: slices of its base64, joined on the
 *  Mac, which keeps the file and answers with where. */
export interface FilePart {
  id: string;
  part: number;
  parts: number;
  /** The file's name on the phone, kept so the agent sees it. */
  name: string;
  data: string;
}

/** A part received, or, for the last, where the file now is on the Mac. */
export interface AttachedFile {
  id: string;
  done: boolean;
  path?: string;
}

/** What a panel command showed. */
export interface CommandPanel {
  command: string;
  text: string;
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
  | "artifacts"
  | "manager_entry"
  | "manager_delta"
  | "manager_history"
  | "manager_status"
  | "manager_brief"
  | "manager_schedules"
  | "manager_settings"
  | "manager_permission"
  | "manager_reset"
  | "knowledge"
  | "keys"
  | "transcript"
  | "voice_account"
  | "commands"
  | "command_panel"
  | "files"
  | "attached";

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
  /** What a recording said, on transcript. */
  transcript?: Transcript;
  /** The Mac's Celery sign-in, on voice_account. */
  voiceAccount?: VoiceAccount;
  /** A session's "/" menu, on commands. */
  commands?: SlashCommand[];
  /** What a panel command showed, on command_panel. */
  panel?: CommandPanel;
  /** Paths in the session's folder for an `@` mention, best first, on files. */
  files?: string[];
  /** A file part received, or where the file now is, on attached. */
  attached?: AttachedFile;
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
  /** One entry in Agentman's conversation, new or finished, on
   *  "manager_entry". */
  entry?: ManagerEntry;
  /** A page of the conversation, oldest first, on "manager_history"; `more`
   *  says older entries remain. */
  entries?: ManagerEntry[];
  more?: boolean;
  /** More text for the entry named by entryId, on "manager_delta". */
  entryId?: string;
  delta?: string;
  /** What Agentman is doing, on "manager_status". */
  managerStatus?: ManagerStatus;
  /** An agent's brief, or all of them, on "manager_brief". */
  brief?: Brief;
  briefs?: Brief[];
  /** Everything Agentman has scheduled, on "manager_schedules". */
  schedules?: ManagerSchedule[];
  /** How Agentman works, on "manager_settings". */
  managerSettings?: ManagerSettings;
  /** What Agentman would do with a request, on "manager_permission". */
  permission?: PermissionCheck;
  /** The user's notes without bodies, or one note whole, on "knowledge". */
  notes?: Note[];
  note?: Note;
  /** The keys Agentman hands out, names only, on "keys". */
  keys?: Key[];
}

/** A key Agentman can hand an agent: the environment variable it becomes,
 *  and who gets it. Its value stays in the Mac's Keychain. */
export interface Key {
  name: string;
  description?: string;
  /** "auto" (any agent), "projects" (agents in the listed folders) or "ask". */
  rule: string;
  projects?: string[];
  /** Whether a value is stored. Read only. */
  set?: boolean;
  updatedAt?: number;
}

/** What Agentman would do with one request. */
export interface PermissionCheck {
  auto: boolean;
  summary: string;
  reason?: string;
}

/** How Agentman works, as the user set it on the Mac or here. */
export interface ManagerSettings {
  chatModel: string;
  briefModel: string;
  effort: string;
  briefs: boolean;
  autoApprove: boolean;
  approve?: { reads: boolean; edits: boolean; commands: boolean; continue: boolean };
  rules?: { action: string; kind: string; pattern: string; folder?: string }[];
  style: string;
  instructions: string;
  /** Whether the chat model's engine has its key on the Mac. Read only. */
  keySet: boolean;
  /** The chat model's engine, and each engine's key. Read only. */
  provider?: string;
  anthropicKeySet?: boolean;
  geminiKeySet?: boolean;
}

/** One of the user's notes, as Agentman keeps them. */
export interface Note {
  name: string;
  /** "shared" (agents read it through Agentman) or "private" (only Agentman). */
  tier: string;
  title: string;
  always?: boolean;
  body?: string;
  updatedAt: number;
}

/**
 * A decision Agentman put in front of the user: an agent's question with its
 * options, an agent's need answered in words, a key it asked for, or a note
 * Agentman suggests keeping.
 */
export interface ManagerCard {
  id: string;
  kind: "question" | "needs_you" | "key" | "note" | string;
  /** "open", "answered" (here), "settled" (elsewhere) or "dismissed". */
  state: string;
  sessionId: string;
  agentName: string;
  title: string;
  detail?: string;
  /** Why Agentman did not answer it itself. */
  reason?: string;
  /** Agentman's latest brief on the agent. */
  context?: string;
  questionId?: string;
  options?: QuestionOption[];
  multiple?: boolean;
  custom?: boolean;
  keyName?: string;
  /** The option Agentman would pick, waiting for the user's OK. */
  suggested?: string;
  /** How it was settled: "You chose Yes". */
  outcome?: string;
  note?: Note;
}

/** One entry in the conversation with Agentman. */
export interface ManagerEntry {
  id: string;
  /** "user", "assistant", "activity", "brief", "agent", "card", "scheduled"
   *  or "error". */
  role: string;
  text: string;
  ts: number;
  /** The agent an activity or brief is about. */
  sessionId?: string;
  /** What an activity did, such as "read_messages". */
  tool?: string;
  /** An assistant entry still being written; deltas fill it. */
  streaming?: boolean;
  /** An image an activity looked at. */
  path?: string;
  /** The images a user's message came with, kept beside the conversation. */
  images?: string[];
  card?: ManagerCard;
  /** The user closed it, here or on the Mac: it is left out. */
  closed?: boolean;
}

export interface ManagerStatus {
  /** "idle", "thinking", "needs_key" or "error". */
  state: string;
  error?: string;
  keySet: boolean;
}

/** Agentman's living summary of one agent. */
export interface Brief {
  sessionId: string;
  name: string;
  summary: string;
  needsYou?: string;
  updatedAt: number;
}

/** A reminder Agentman left itself, or a watch on an agent. */
export interface ManagerSchedule {
  id: string;
  kind: "reminder" | "watch" | string;
  text: string;
  /** When a reminder next fires, or a watch gives up, epoch milliseconds. */
  due: number;
  /** A repeating reminder's interval, in seconds. */
  every?: number;
  agentName?: string;
  condition?: string;
  createdAt: number;
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
  /** Agentman is on: it hears every agent and speaks for them. */
  agentman?: boolean;
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
    "manager_entry", "manager_delta", "manager_history", "manager_status",
    "manager_brief", "manager_schedules", "manager_settings",
    "manager_permission", "manager_reset", "knowledge", "keys",
    "transcript", "voice_account", "commands", "command_panel", "files", "attached",
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
    case "manager_entry":
      if (!isManagerEntry(value.entry)) return null;
      break;
    case "manager_delta":
      if (!boundedString(value.entryId, 512, true) || !boundedString(value.delta, 512 * 1024)) return null;
      break;
    case "manager_history":
      // An entry the app cannot read is left out, not the whole page.
      value.entries ??= [];
      if (!Array.isArray(value.entries) || value.entries.length > 5_000) return null;
      value.entries = value.entries.filter(isManagerEntry);
      if (value.more !== undefined && typeof value.more !== "boolean") delete value.more;
      break;
    case "manager_status":
      if (!isRecord(value.managerStatus) || !boundedString(value.managerStatus.state, 64, true)) return null;
      if (!optionalBoundedString(value.managerStatus.error, 64 * 1024)) delete value.managerStatus.error;
      value.managerStatus.keySet = value.managerStatus.keySet === true;
      break;
    case "manager_brief":
      if (value.brief !== undefined && !isBrief(value.brief)) delete value.brief;
      if (value.briefs !== undefined) {
        if (!Array.isArray(value.briefs) || value.briefs.length > 10_000) return null;
        value.briefs = value.briefs.filter(isBrief);
      }
      break;
    case "manager_settings":
      if (!isRecord(value.managerSettings)) return null;
      value.managerSettings.keySet = value.managerSettings.keySet === true;
      if (value.managerStatus !== undefined &&
          (!isRecord(value.managerStatus) || !boundedString(value.managerStatus.state, 64, true))) {
        delete value.managerStatus;
      }
      break;
    case "manager_permission":
      if (!isRecord(value.permission) || typeof value.permission.auto !== "boolean" ||
          !boundedString(value.permission.summary, 64 * 1024) ||
          !optionalBoundedString(value.permission.reason, 64 * 1024)) return null;
      break;
    case "knowledge":
      if (value.notes !== undefined) {
        if (!Array.isArray(value.notes) || value.notes.length > 10_000) return null;
        value.notes = value.notes.filter(isNote);
      }
      if (value.note !== undefined && !isNote(value.note)) delete value.note;
      break;
    case "transcript":
      if (!isRecord(value.transcript) || !boundedString(value.transcript.id, 64, true) ||
          typeof value.transcript.done !== "boolean" ||
          !optionalBoundedString(value.transcript.text, 256 * 1024)) return null;
      break;
    case "voice_account":
      if (!isRecord(value.voiceAccount) || typeof value.voiceAccount.signedIn !== "boolean" ||
          !optionalBoundedString(value.voiceAccount.email, 320) ||
          !optionalBoundedString(value.voiceAccount.plan, 64)) return null;
      break;
    case "commands":
      value.commands ??= [];
      if (!Array.isArray(value.commands) || value.commands.length > 2_000) return null;
      value.commands = value.commands.filter(isSlashCommand);
      break;
    case "attached":
      if (!isRecord(value.attached) || !boundedString(value.attached.id, 64, true) ||
          typeof value.attached.done !== "boolean" || !optionalBoundedString(value.attached.path, 4096)) return null;
      break;
    case "files":
      value.files ??= [];
      if (!Array.isArray(value.files) || value.files.length > 1_000) return null;
      value.files = value.files.filter((path: unknown) => boundedString(path, 4096, true));
      break;
    case "command_panel":
      if (!isRecord(value.panel) || !boundedString(value.panel.command, 64, true) ||
          !boundedString(value.panel.text, 256 * 1024)) return null;
      break;
    case "keys":
      value.keys ??= [];
      if (!Array.isArray(value.keys) || value.keys.length > 1_000) return null;
      value.keys = value.keys.filter(isKey);
      break;
    case "manager_schedules":
      value.schedules ??= [];
      if (!Array.isArray(value.schedules) || value.schedules.length > 1_000) return null;
      value.schedules = value.schedules.filter(isSchedule);
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
      // An image kept with Agentman's conversation belongs to no session.
      if (isRecord(value.workspace) && value.workspace.sessionId === undefined) value.workspace.sessionId = "agentman";
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
      // Empty for Agentman's own pictures, which belong to no agent.
      !boundedString(value.sessionId, 512) || !optionalBoundedString(value.path, 4096) ||
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
      boundedArray(value.queued, MAX_QUEUED, (text): text is string => boundedString(text, 64 * 1024, true))) &&
    (value.queuedMessageIds === undefined ||
      boundedArray(value.queuedMessageIds, MAX_QUEUED * 8, (id): id is string => boundedString(id, 512, true))) &&
    (value.canSendQueued === undefined || typeof value.canSendQueued === "boolean") &&
    (value.previous === undefined || boundedString(value.previous, 512, true));
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

function isNote(value: unknown): value is Note {
  return isRecord(value) && boundedString(value.name, 512) && boundedString(value.tier, 64, true) &&
    boundedString(value.title, 4096) && optionalBoundedString(value.body, 256 * 1024) &&
    (value.always === undefined || typeof value.always === "boolean");
}

function isSlashCommand(value: unknown): value is SlashCommand {
  return isRecord(value) && boundedString(value.name, 128, true) && !/\s/.test(value.name) &&
    optionalBoundedString(value.description, 1024) && optionalBoundedString(value.argumentHint, 256) &&
    optionalBoundedString(value.source, 32) && (value.panel === undefined || typeof value.panel === "boolean");
}

function isKey(value: unknown): value is Key {
  return isRecord(value) && boundedString(value.name, 256, true) && boundedString(value.rule, 64, true) &&
    optionalBoundedString(value.description, 4096) &&
    (value.set === undefined || typeof value.set === "boolean") &&
    (value.projects === undefined ||
      boundedArray(value.projects, 256, (project): project is string => boundedString(project, 4096, true)));
}

function isManagerCard(value: unknown): value is ManagerCard {
  if (!isRecord(value) || !boundedString(value.id, 512, true) || !boundedString(value.kind, 64, true) ||
      !boundedString(value.state, 64, true) || !boundedString(value.title, 64 * 1024)) return false;
  value.sessionId ??= "";
  value.agentName ??= "";
  if (!boundedString(value.sessionId, 512) || !boundedString(value.agentName, 4096)) return false;
  for (const key of ["detail", "reason", "context", "questionId", "keyName", "suggested", "outcome"] as const) {
    if (!optionalBoundedString(value[key], 256 * 1024)) delete value[key];
  }
  // A part the app cannot read is dropped, not the card with it.
  if (value.note !== undefined && !isNote(value.note)) delete value.note;
  if (value.options !== undefined && !isQuestion({ id: "card", prompt: "", options: value.options })) delete value.options;
  if (value.multiple !== undefined && typeof value.multiple !== "boolean") delete value.multiple;
  if (value.custom !== undefined && typeof value.custom !== "boolean") delete value.custom;
  return true;
}

function isManagerEntry(value: unknown): value is ManagerEntry {
  if (!isRecord(value) || !boundedString(value.id, 512, true) || !boundedString(value.role, 64, true) ||
      !finiteNumber(value.ts) || !optionalBoundedString(value.text, 512 * 1024)) return false;
  value.text ??= "";
  for (const key of ["sessionId", "tool", "path"] as const) {
    if (!optionalBoundedString(value[key], 4096)) delete value[key];
  }
  if (value.streaming !== undefined && typeof value.streaming !== "boolean") delete value.streaming;
  if (value.closed !== undefined && typeof value.closed !== "boolean") delete value.closed;
  if (value.images !== undefined &&
      !boundedArray(value.images, 16, (name): name is string => boundedString(name, 512, true))) delete value.images;
  if (value.card !== undefined && !isManagerCard(value.card)) return false;
  return true;
}

function isBrief(value: unknown): value is Brief {
  if (!isRecord(value) || !boundedString(value.sessionId, 512, true) || !boundedString(value.name, 4096) ||
      !boundedString(value.summary, 64 * 1024)) return false;
  if (!optionalBoundedString(value.needsYou, 64 * 1024)) delete value.needsYou;
  if (!finiteNumber(value.updatedAt)) value.updatedAt = 0;
  return true;
}

function isSchedule(value: unknown): value is ManagerSchedule {
  return isRecord(value) && boundedString(value.id, 512, true) && boundedString(value.kind, 64, true) &&
    boundedString(value.text, 64 * 1024) && finiteNumber(value.due) &&
    optionalBoundedString(value.agentName, 4096) && optionalBoundedString(value.condition, 64) &&
    (value.every === undefined || finiteNumber(value.every));
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
