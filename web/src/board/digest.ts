// What a card says about an agent, read from the end of its conversation
// (Stores/Board.swift's BoardDigest): the steps of the turn it is on, the
// command it ran last, how far through its checklist it is, and what it
// last said. Pure, so its tests run under Node (sections.test.mts).

import type { Message } from "@shared/protocol";

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

export interface BoardDigest {
  steps: BoardStep[];
  command?: string;
  progress?: BoardProgress;
  /** What it said last: where it began, which sums up finished work, and
   *  where it ended, which is where a question comes. */
  said?: string;
  asked?: string;
  turnStarted?: number;
}

/** How much of the conversation is read: the end is all a card shows, and
 *  this runs each time any agent says something. */
const WINDOW = 80;
const STEPS_SHOWN = 3;

// The Mac's ToolKind (AgentThreadView.swift): its plan and command names.
const PLAN = /^(todowrite|update_plan|todo_write)$/i;
const COMMAND = /^(bash|shell|exec_command|run_shell_command|local_shell|terminal|command)$/i;

/** A step's or command's words without a leading "$ " and the like
 *  (thread/items.ts's tidy, given here so this stays pure). */
export type Tidy = (text: string) => string;

export function makeDigest(messages: readonly Message[], tidy: Tidy = (t) => t.trim()): BoardDigest {
  const digest: BoardDigest = { steps: [] };
  const tail = messages.slice(-WINDOW).filter((m) => m.isSidechain !== true);
  let start = -1;
  for (let i = tail.length - 1; i >= 0; i--) {
    if (tail[i].role === "user") {
      start = i;
      break;
    }
  }
  if (start >= 0) digest.turnStarted = tail[start].ts;
  const turn = start >= 0 ? tail.slice(start + 1) : tail;

  const tools = turn.filter((m) => m.tool && !PLAN.test(m.tool.name));
  digest.steps = tools.slice(-STEPS_SHOWN).map((m) => step(m, tidy)).filter((s): s is BoardStep => !!s);
  const command = [...tools].reverse().find((m) => COMMAND.test(m.tool?.name ?? ""))?.tool?.summary;
  if (command && tidy(command)) digest.command = tidy(command);

  for (let i = tail.length - 1; i >= 0; i--) {
    const m = tail[i];
    if (!digest.progress && m.tool && PLAN.test(m.tool.name) && m.text) digest.progress = checklist(m.text);
    if (digest.said === undefined && m.role === "assistant" && m.text) {
      const found = paragraphs(m.text);
      digest.said = found[0];
      digest.asked = found[found.length - 1];
    }
    if (digest.progress && digest.said !== undefined) break;
  }
  return digest;
}

function step(m: Message, tidy: Tidy): BoardStep | null {
  const tool = m.tool;
  if (!tool) return null;
  const text = tool.title ?? (tool.summary !== undefined ? tidy(tool.summary) : tool.name);
  if (!text) return null;
  return { id: m.id, text, state: tool.status === "running" ? "running" : tool.status === "error" ? "failed" : "done" };
}

/** How far through a checklist, written one item a line, each behind a mark
 *  as the parsers write them. Nothing for anything else: no bar is better
 *  than an invented one. */
export function checklist(text: string): BoardProgress | undefined {
  const progress: BoardProgress = { done: 0, total: 0 };
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    const first = [...line][0];
    const item = line.slice(first.length).trim();
    if (first === "✔" || first === "✓") {
      progress.done++;
      progress.total++;
    } else if (first === "◐") {
      progress.total++;
      if (progress.now === undefined) progress.now = item;
    } else if (first === "○") {
      progress.total++;
    } else if (first === "✕") {
      continue;
    } else {
      return undefined;
    }
  }
  return progress.total >= 2 ? progress : undefined;
}

/** What it said, paragraph by paragraph, without code, headings, tables or a
 *  list's bullets. */
export function paragraphs(text: string): string[] {
  const out: string[] = [];
  let current: string[] = [];
  let fenced = false;
  const close = () => {
    if (current.length) out.push(current.join(" "));
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
    for (const bullet of ["- ", "* ", "> "]) if (line.startsWith(bullet)) line = line.slice(bullet.length);
    current.push(line);
  }
  close();
  return out;
}
