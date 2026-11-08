import { isHidden, type Dismissals } from "./dismissRules.ts";
import type { ManagerEntry, Message, Question, Session } from "./protocol.ts";
import { folderName } from "./session-view.ts";

/**
 * The Board's rules, the Mac's (desktop/Agentman/Stores/Board.swift): every
 * agent sorted by what it wants from the user, the line that says so, and
 * what a card says about an agent, read from the end of its conversation.
 * Kept free of React so they can be tested on Node.
 */

/** The agent's state as the Board sorts it: waiting on the user whenever a
 *  question is up. */
export type BoardColumn = "needsYou" | "working" | "idle";

export function boardColumn(session: Pick<Session, "state" | "question">): BoardColumn {
  if (session.question || session.state === "waiting_input") return "needsYou";
  return session.state === "busy" ? "working" : "idle";
}

/** Idle this long, an agent has left the Board by itself. */
const ARCHIVE_AFTER_MS = 3 * 24 * 60 * 60 * 1000;

export interface BoardSections {
  needsYou: Session[];
  working: Session[];
  idle: Session[];
  /** Put away by the user, and left out of the columns. */
  hidden: Session[];
}

export interface BoardOptions {
  /** Agents hidden from the Board, by id. */
  hidden?: ReadonlySet<string>;
  /** Agents Agentman has put a card up about, which need the user whatever
   *  their state says. */
  waiting?: ReadonlySet<string>;
  /** Agents swiped away from the list, which stay away until they do
   *  something. */
  dismissals?: Dismissals;
  /** Narrows the Board to one folder. */
  inFolder?: (cwd: string) => boolean;
  now?: number;
}

/**
 * Sorts the agents. Needing the user beats being hidden or swiped away; an
 * agent that has ended, or has sat idle for days, is not on the Board at all.
 */
export function boardSections(sessions: readonly Session[], options: BoardOptions = {}): BoardSections {
  const { hidden, waiting, dismissals = {}, inFolder, now = Date.now() } = options;
  const sections: BoardSections = { needsYou: [], working: [], idle: [], hidden: [] };
  for (const session of sessions) {
    if (session.state === "ended" || (inFolder && !inFolder(session.cwd))) continue;
    const column = boardColumn(session);
    if (column === "needsYou" || waiting?.has(session.id)) {
      sections.needsYou.push(session);
    } else if (isHidden(session, dismissals) || (column === "idle" && now - session.lastActivityAt > ARCHIVE_AFTER_MS)) {
      continue;
    } else if (hidden?.has(session.id)) {
      sections.hidden.push(session);
    } else if (column === "working") {
      sections.working.push(session);
    } else {
      sections.idle.push(session);
    }
  }
  const recent = (a: Session, b: Session) => b.lastActivityAt - a.lastActivityAt || b.id.localeCompare(a.id);
  // A working agent's last activity moves every second: by when it started,
  // its card stays put.
  const started = (a: Session, b: Session) => b.startedAt - a.startedAt || b.id.localeCompare(a.id);
  sections.needsYou.sort(recent);
  sections.working.sort(started);
  sections.idle.sort(recent);
  sections.hidden.sort(recent);
  return sections;
}

/** The hidden agents that stay hidden: one that needs the user is back for
 *  good, not only while it waits. */
export function stillHidden(hidden: ReadonlySet<string>, sessions: readonly Session[]): Set<string> {
  const next = new Set(hidden);
  for (const session of sessions) {
    if (next.has(session.id) && boardColumn(session) === "needsYou") next.delete(session.id);
  }
  return next;
}

/** The title of the Board, and the line under it. */
export function boardBrief(sections: BoardSections, connected: boolean): { title: string; detail: string } {
  if (!connected) return { title: "Waiting to reach your agents.", detail: "" };
  const { needsYou, working, idle, hidden } = sections;
  const counts = [working.length > 0 ? `${working.length} working` : "", idle.length > 0 ? `${idle.length} idle` : ""]
    .filter(Boolean)
    .join(", ");
  if (needsYou.length > 0) {
    return {
      title: needsYou.length === 1 ? "1 agent is waiting on you." : `${needsYou.length} agents are waiting on you.`,
      detail: counts ? `The rest are fine: ${counts}.` : "",
    };
  }
  if (working.length > 0) {
    const who = working.length === 1 ? "1 agent is working" : `${working.length} agents are working`;
    return { title: "Nothing needs you.", detail: `${who}${idle.length > 0 ? `, ${idle.length} idle` : ""}.` };
  }
  if (idle.length === 0 && hidden.length === 0) {
    return { title: "No agents running.", detail: "Start one and it shows up here." };
  }
  return { title: "All quiet. Nothing needs you.", detail: idle.length > 0 ? `${idle.length} idle.` : "" };
}

/** Folders Agentman made as git worktrees: ~/Agentman/Worktrees/<repo>/<name>. */
export function worktreeParts(path: string): { repo: string; name: string } | null {
  const match = /^(?:\/Users\/[^/]+|\/home\/[^/]+|~)\/Agentman\/Worktrees\/([^/]+)\/([^/]+)(?:\/|$)/.exec(path);
  return match ? { repo: match[1], name: match[2] } : null;
}

/** A folder's name as a card shows it: a worktree's as its repository's and
 *  its own, "agentman · rate-limits". */
export function boardFolderLabel(path: string): string {
  const parts = worktreeParts(path);
  return parts ? `${parts.repo} · ${parts.name}` : folderName(path);
}

/** A tool's name, as the kinds the Board cares about. Names are the CLIs' own. */
const COMMAND_TOOLS = new Set(["bash", "shell", "exec_command", "run_shell_command", "local_shell", "terminal", "command"]);
const PLAN_TOOLS = new Set(["todowrite", "update_plan", "todo_write"]);

const isCommandTool = (name?: string) => COMMAND_TOOLS.has((name ?? "").toLowerCase());
const isPlanTool = (name?: string) => PLAN_TOOLS.has((name ?? "").toLowerCase());

/** A target made readable: home as ~, an agent's own temporary folder left
 *  out, and whitespace on one line. */
export function tidyTarget(text: string): string {
  return text
    .replace(/\/Users\/[^/\s]+/g, "~")
    .replace(/\/(?:private\/)?tmp\/claude-\d+\/[^/\s]+\/[0-9a-f-]{36}\//g, "")
    .split(/[\r\n]+/)
    .join(" ")
    .trim();
}

export interface BoardStep {
  id: string;
  text: string;
  state: "done" | "running" | "failed";
}

export interface BoardProgress {
  done: number;
  total: number;
  /** The item it is on. */
  now?: string;
}

/** What a card says about an agent: the steps of the turn it is on, the
 *  command it ran last, how far through its checklist it is, and what it
 *  last said. */
export interface BoardDigest {
  steps: BoardStep[];
  command?: string;
  progress?: BoardProgress;
  /** What it said last: where it began, which sums up finished work. */
  said?: string;
  /** And where it ended, which is where a question comes. */
  asked?: string;
  /** When the turn it is on began. */
  turnStarted?: number;
}

/** How much of a conversation is read: the end is all a card shows. */
const DIGEST_WINDOW = 80;
const STEPS_SHOWN = 3;

export function boardDigest(messages: readonly Message[]): BoardDigest {
  const digest: BoardDigest = { steps: [] };
  const tail = messages.slice(-DIGEST_WINDOW).filter((message) => message.isSidechain !== true);
  const start = tail.map((message) => message.role).lastIndexOf("user");
  if (start !== -1) digest.turnStarted = tail[start].ts;
  const turn = start !== -1 ? tail.slice(start + 1) : tail;

  const tools = turn.filter((message) => message.tool && !isPlanTool(message.tool.name));
  digest.steps = tools.slice(-STEPS_SHOWN).flatMap((message) => {
    const step = boardStep(message);
    return step ? [step] : [];
  });
  const lastCommand = [...tools].reverse().find((message) => isCommandTool(message.tool?.name));
  const command = lastCommand?.tool?.summary ? tidyTarget(lastCommand.tool.summary) : "";
  if (command) digest.command = command;

  for (const message of [...tail].reverse()) {
    if (!digest.progress && isPlanTool(message.tool?.name) && message.text) {
      const progress = checklist(message.text);
      if (progress) digest.progress = progress;
    }
    if (digest.said === undefined && message.role === "assistant" && message.text) {
      const said = paragraphs(message.text);
      if (said.length > 0) {
        digest.said = said[0];
        digest.asked = said[said.length - 1];
      }
    }
    if (digest.progress && digest.said !== undefined) break;
  }
  return digest;
}

function boardStep(message: Message): BoardStep | null {
  const tool = message.tool;
  if (!tool) return null;
  const text = tool.title ?? (tool.summary ? tidyTarget(tool.summary) : tool.name);
  if (!text) return null;
  return { id: message.id, text, state: tool.status === "running" ? "running" : tool.status === "error" ? "failed" : "done" };
}

/**
 * How far through a checklist, written one item a line, each behind a mark as
 * the parsers write them. Undefined for anything else: no bar is better than
 * an invented one.
 */
export function checklist(text: string): BoardProgress | undefined {
  const progress: BoardProgress = { done: 0, total: 0 };
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    const item = line.slice(1).trim();
    switch (line[0]) {
      case "✔":
      case "✓":
        progress.done += 1;
        progress.total += 1;
        break;
      case "◐":
        progress.total += 1;
        if (!progress.now) progress.now = item;
        break;
      case "○":
        progress.total += 1;
        break;
      case "✕":
        break;
      default:
        return undefined;
    }
  }
  return progress.total >= 2 ? progress : undefined;
}

/** What it said, paragraph by paragraph, without code, headings, tables or a
 *  list's bullets. */
export function paragraphs(text: string): string[] {
  const found: string[] = [];
  let current: string[] = [];
  let fenced = false;
  const close = () => {
    if (current.length > 0) found.push(current.join(" "));
    current = [];
  };
  for (const raw of text.split("\n")) {
    let line = raw.trim();
    if (line.startsWith("```")) {
      fenced = !fenced;
      close();
      continue;
    }
    if (fenced) continue;
    if (!line || line.startsWith("#") || line.startsWith("|")) {
      close();
      continue;
    }
    for (const bullet of ["- ", "* ", "> "]) {
      if (line.startsWith(bullet)) line = line.slice(bullet.length);
    }
    current.push(line);
  }
  close();
  return found;
}

/** A line of what an agent wrote, without its markdown: cards are plain. */
export function plainInline(text: string): string {
  return text
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/(\*\*|__)(.+?)\1/g, "$2")
    .replace(/(^|[\s(])[*_](?=\S)(.+?)(?<=\S)[*_](?=$|[\s).,:;!?])/g, "$1$2")
    .replace(/`([^`]*)`/g, "$1")
    .replace(/\s*\n\s*/g, " ");
}

/**
 * Whether a question's prompt only says its command again ("Run git push
 * origin main?" under that command): then it is left out, and the command is
 * said once.
 */
export function promptRepeatsDetail(question: Pick<Question, "prompt" | "detail">): boolean {
  const detail = question.detail?.trim();
  if (!detail || detail.includes("\n")) return false;
  const prompt = question.prompt.trim();
  const at = prompt.toLowerCase().indexOf(detail.toLowerCase());
  if (at === -1) return false;
  const rest = `${prompt.slice(0, at)} ${prompt.slice(at + detail.length)}`
    .replace(/[\p{P}\p{S}]/gu, " ")
    .trim();
  return rest === "" || rest.split(/\s+/).length <= 2;
}

/**
 * The agents Agentman's latest answer is about: those it looked into and
 * those it names.
 */
export function mentionedSessions(entries: readonly ManagerEntry[], sessions: readonly Session[]): Set<string> {
  const start = entries.map((entry) => entry.role).lastIndexOf("user") + 1;
  const turn = entries.slice(start);
  const ids = new Set(turn.flatMap((entry) => (entry.role === "activity" && entry.sessionId ? [entry.sessionId] : [])));
  const said = turn.filter((entry) => entry.role === "assistant").map((entry) => entry.text).join("\n").toLowerCase();
  for (const session of sessions) {
    if (session.name.length >= 4 && said.includes(session.name.toLowerCase())) ids.add(session.id);
  }
  return ids;
}

/**
 * Whether text is long enough to be cut, by about how many characters fit a
 * card's line at this width: enough to know when to offer More without
 * measuring every line.
 */
export function boardTextIsLong(text: string, lines: number, cardWidth: number): boolean {
  const lineLength = Math.max(24, Math.round((cardWidth - 32) / 7.1));
  return text.length > lines * lineLength;
}
