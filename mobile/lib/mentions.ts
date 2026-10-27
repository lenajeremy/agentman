/**
 * `@` and part of a file's name in a message box: the session's files that
 * match, to name one as `@path`. The agent reads what the message names
 * (Claude Code attaches the file as the message arrives), so the box only
 * ever holds the path. The Mac finds the files, in the session's folder.
 */

import type { DaemonEvent, Request } from "./protocol";

type Ask = (request: Request, timeoutMs?: number) => Promise<DaemonEvent>;

/** The `@word` being typed at the caret: where it starts, and what follows
 *  the `@`. */
export interface MentionAt {
  start: number;
  end: number;
  query: string;
}

/**
 * The mention the caret is at the end of, if it is: a word that starts with
 * `@`, at the start of the box or after a space, with no other `@` in it.
 */
export function mentionAt(text: string, caret: number): MentionAt | null {
  if (caret < 0 || caret > text.length) return null;
  // Only at the end of a word: a caret mid-word is editing, not naming.
  if (caret < text.length && !/\s/.test(text[caret])) return null;
  let start = caret;
  while (start > 0 && !/\s/.test(text[start - 1])) start -= 1;
  const word = text.slice(start, caret);
  if (!word.startsWith("@") || word.slice(1).includes("@")) return null;
  return { start, end: caret, query: word.slice(1) };
}

/** The box with a picked file in place of the `@word`, and where the caret
 *  goes: after the space that ends the mention. */
export function withMention(text: string, at: MentionAt, path: string): { text: string; caret: number } {
  const inserted = `@${path} `;
  const rest = text.slice(at.end).replace(/^ /, "");
  return { text: text.slice(0, at.start) + inserted + rest, caret: at.start + inserted.length };
}

/** The session's files matching what follows the `@`; with nothing typed,
 *  the files changed since the last commit. */
export async function findFiles(ask: Ask, sessionId: string, query: string): Promise<string[]> {
  const reply = await ask({ type: "find_files", sessionId, text: query });
  return reply.files ?? [];
}

/** A path as a name and the folder it is in, for a row. */
export function splitPath(path: string): { name: string; folder: string } {
  const slash = path.lastIndexOf("/");
  return slash < 0 ? { name: path, folder: "" } : { name: path.slice(slash + 1), folder: path.slice(0, slash) };
}
