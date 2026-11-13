// ↑ and ↓ in an agent's message box, as in a terminal (Stores/
// PromptHistory.swift): back through what was said to the agent, newest
// first, and forward again to the message being written.
//
// It starts from the thread's own messages, already loaded, and asks the
// daemon for older ones (fetch_prompts) only when ↑ goes past them, a page
// of prompts at a time, never the whole conversation. What it holds is let
// go once the box is back on the draft, the message is sent, or the words
// are changed.

import type { Message } from "@shared/protocol";
import { isCompactionSummary } from "./compaction.ts";
import { isKeptImage } from "./keptImage.ts";

/** Prompts newest first, with where the thread or the daemon can read
 *  further back from. */
export interface Prompts {
  prompts: string[];
  cursor?: string;
  hasMore: boolean;
}

type Show = (text: string) => void;

export class PromptHistory {
  /** How many pages of prompts one ↑ asks for before giving up, for a long
   *  stretch in which the user said nothing. */
  static fetchesPerStep = 4;
  /** How close to the last prompt held ↑ comes before the next page is
   *  read, so reading it is done before it is wanted. */
  static readAheadWithin = 5;

  /** The prompts in the agent's thread as loaded, and where its older pages
   *  start. */
  loaded: (sessionId: string) => Prompts = () => ({ prompts: [], hasMore: false });
  /** Older prompts from before a cursor; null when the daemon can't say. */
  fetch: (sessionId: string, cursor: string) => Promise<Prompts | null> = async () => null;

  /** The prompts as far back as ↑ has gone, newest first. */
  prompts: string[] = [];
  /** Which of them the box shows; null while it shows the draft. */
  position: number | null = null;
  /** What the box held before ↑, given back after the newest prompt. */
  draft = "";
  /** The words the box was given, as it reads them back: anything else
   *  there means the user changed them, and the history starts again. */
  shown: string | null = null;

  private session: string | null = null;
  private cursor: string | undefined;
  private hasMore = false;
  private fetching = false;
  /** Bumped by reset, so a page still being read is let go. */
  private generation = 0;
  /** A step ↑ asked for past the prompts held, made once they are read. */
  private waiting: { step: number; show: Show } | null = null;

  /** The agent whose prompts these are; another starts again. */
  get sessionId(): string | null {
    return this.session;
  }
  set sessionId(id: string | null) {
    if (id === this.session) return;
    this.session = id;
    this.reset();
  }

  get isBrowsing(): boolean {
    return this.position !== null;
  }

  /** Whether the box holds a prompt from the history, as it was given. */
  shows(text: string): boolean {
    return this.position !== null && text === this.shown;
  }

  /** The box shows text it was given: read back as the box reads it. */
  showing(text: string) {
    this.shown = text;
  }

  /** ↑ with text in the box: the prompt before the one shown, or the newest
   *  one before the draft, handed to show now or once the daemon has read
   *  further back. False when there is nothing further back, so the key
   *  does what it does anyway. */
  older(text: string, show: Show): boolean {
    const session = this.session;
    if (!session) return false;
    if (this.position !== null && text !== this.shown) this.reset();
    if (this.position === null) {
      const start = this.loaded(session);
      this.prompts = [];
      this.add(start.prompts);
      this.cursor = start.cursor;
      this.hasMore = start.hasMore;
      this.draft = text;
    }
    const next = (this.position ?? -1) + 1;
    if (next < this.prompts.length) {
      this.position = next;
      show(this.prompts[next]);
      this.readAheadIfNear();
      return true;
    }
    if (!this.hasMore || !this.cursor) {
      if (this.position === null) this.reset();
      return false;
    }
    // The page being read moves the box when it comes.
    this.waiting = { step: next, show };
    this.readAhead();
    return true;
  }

  /** The user changed the words: a step ↑ asked for that is still being read
   *  isn't made, so their words stay. */
  edited() {
    this.waiting = null;
  }

  /** ↓: the prompt after the one shown, or the draft after the newest. False
   *  when the box is on the draft already, or its words were changed, so the
   *  key does what it does anyway. */
  newer(text: string, show: Show): boolean {
    this.waiting = null;
    const position = this.position;
    if (position === null) return false;
    if (text !== this.shown) {
      this.reset();
      return false;
    }
    if (position === 0) {
      const draft = this.draft;
      this.reset();
      show(draft);
    } else {
      this.position = position - 1;
      show(this.prompts[position - 1]);
    }
    return true;
  }

  /** Back on the draft, holding nothing. */
  reset() {
    this.generation += 1;
    this.fetching = false;
    this.waiting = null;
    this.prompts = [];
    this.position = null;
    this.draft = "";
    this.shown = null;
    this.cursor = undefined;
    this.hasMore = false;
  }

  /** Older prompts after those held, a prompt said twice in a row once. */
  private add(older: string[]) {
    for (const prompt of older) {
      if (prompt !== this.prompts.at(-1)) this.prompts.push(prompt);
    }
  }

  private readAheadIfNear() {
    if (this.prompts.length - (this.position ?? -1) - 1 <= PromptHistory.readAheadWithin) this.readAhead();
  }

  /** Reads the next page of older prompts, unless one is being read or none
   *  is left: for a long stretch with none, up to fetchesPerStep pages. */
  private readAhead() {
    const session = this.session;
    if (this.fetching || !this.hasMore || !this.cursor || !session) return;
    const had = this.prompts.length;
    const generation = this.generation;
    this.fetching = true;
    const current = () => this.generation === generation && this.session === session;
    void (async () => {
      let tries = 0;
      while (current() && this.prompts.length === had && this.hasMore && this.cursor && tries < PromptHistory.fetchesPerStep) {
        tries += 1;
        let more: Prompts | null = null;
        try {
          more = await this.fetch(session, this.cursor);
        } catch {
          more = null;
        }
        if (!current()) return;
        if (!more) {
          this.hasMore = false;
          break;
        }
        this.add(more.prompts);
        this.cursor = more.cursor;
        this.hasMore = more.hasMore;
      }
      if (!current()) return;
      this.fetching = false;
      const waiting = this.waiting;
      if (!waiting) return;
      if (waiting.step < this.prompts.length) {
        this.waiting = null;
        this.position = waiting.step;
        waiting.show(this.prompts[waiting.step]);
        this.readAheadIfNear();
      } else if (this.hasMore && this.prompts.length > had) {
        this.readAhead();
      } else {
        this.waiting = null;
        if (this.position === null) this.reset();
      }
    })();
  }
}

/** The words of a prompt as the box takes them again: without the pictures
 *  that went with it, which come back as their paths the daemon typed ahead
 *  of the words or as "[image]" where the agent read one; null for pictures
 *  alone. Files named with @ stay, as the box shows them as chips. */
export function promptWords(text: string): string | null {
  let rest = text.trimStart();
  for (;;) {
    const end = rest.search(/\s/);
    const token = end < 0 ? rest : rest.slice(0, end);
    if (!token || !isKeptImage(token)) break;
    rest = (end < 0 ? "" : rest.slice(end)).trimStart();
  }
  const words = rest
    .split("\n")
    .filter((line) => line.trim() !== "[image]")
    .join("\n")
    .trim();
  return words || null;
}

/** A message the user wrote to the agent, for its history: theirs, with
 *  words, not a tool's result, a subagent's, or what the agent writes in the
 *  user's place: the summary of an earlier conversation, or Claude Code's
 *  "[Request interrupted by user]". As the daemon's isPrompt. */
export function isPrompt(message: Message): boolean {
  if (message.role !== "user" || message.tool || message.isSidechain) return false;
  const text = (message.text ?? "").trim();
  return text !== "" && !isCompactionSummary(text) && !text.startsWith("[Request interrupted by user");
}

/** The words of what was said, newest first, for the history. */
export function promptsOf(messages: readonly Message[], queued: readonly string[] = []): string[] {
  const said = messages.filter(isPrompt).map((m) => promptWords(m.text ?? ""));
  const waiting = queued.map(promptWords);
  return [...said, ...waiting].filter((w): w is string => w !== null).reverse();
}
