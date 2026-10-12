/**
 * Small rules for showing commit history, kept out of the screens so they can
 * be tested on Node like the other pure rules in this directory.
 */
import type { WorkspaceCommit } from "./protocol";

/** The requests that read history. A Mac older than them rejects each. */
export type HistoryRequest = "list_commits" | "commit_files" | "commit_diff" | "list_branches";

export function isHistoryRequest(type: string): type is HistoryRequest {
  return type === "list_commits" || type === "commit_files" || type === "commit_diff" ||
    type === "list_branches";
}

/**
 * A daemon older than commit history refuses the request by name, which is
 * accurate and useless on a phone. Say what fixes it instead.
 */
export function historyError(message: string): string {
  return message.includes("unsupported request")
    ? "Update agentman on your Mac to see commits from your phone."
    : message;
}

/** Seven characters, the length git itself abbreviates to in a small repo. */
export function shortHash(hash: string): string {
  return hash.slice(0, 7);
}

const months = [
  "Jan", "Feb", "Mar", "Apr", "May", "Jun",
  "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
] as const;
const weekdays = [
  "Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday",
] as const;

/** "14:05". The day is said once, by the heading a commit sits under. */
export function clockTime(epochMillis: number): string {
  const when = new Date(epochMillis);
  return `${String(when.getHours()).padStart(2, "0")}:${String(when.getMinutes()).padStart(2, "0")}`;
}

/** Today, Yesterday, a weekday within the week, and a date after that. */
export function dayLabel(epochMillis: number, now = new Date()): string {
  const when = new Date(epochMillis);
  const midnight = (date: Date) =>
    new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
  // Rounded rather than floored: a day that crosses a clock change is 23 or
  // 25 hours long.
  const days = Math.round((midnight(now) - midnight(when)) / 86_400_000);
  if (days === 0) return "Today";
  if (days === 1) return "Yesterday";
  if (days > 1 && days < 7) return weekdays[when.getDay()];
  const day = `${when.getDate()} ${months[when.getMonth()]}`;
  return when.getFullYear() === now.getFullYear() ? day : `${day} ${when.getFullYear()}`;
}

export interface CommitDay {
  /** Unique within one list, for React. */
  key: string;
  label: string;
  commits: WorkspaceCommit[];
}

/**
 * Commits under a heading for the day each was written, in the order git
 * gave them. Only neighbours are grouped: git orders by when a commit was
 * made, a rebased commit keeps the day it was written, and moving it to sit
 * with that day would put it out of order.
 */
export function groupByDay(commits: WorkspaceCommit[], now = new Date()): CommitDay[] {
  const days: CommitDay[] = [];
  for (const commit of commits) {
    const label = dayLabel(commit.authoredAt, now);
    const last = days[days.length - 1];
    if (last && last.label === label) last.commits.push(commit);
    else days.push({ key: `${label}:${days.length}`, label, commits: [commit] });
  }
  return days;
}

/** One or two letters for a person's mark: first and last name. */
export function initials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return "?";
  const first = Array.from(words[0])[0] ?? "";
  const last = words.length > 1 ? (Array.from(words[words.length - 1])[0] ?? "") : "";
  return (first + last).toUpperCase();
}

export interface Trailer {
  key: string;
  value: string;
}

const TRAILER_LINE = /^([A-Za-z][\w-]*):\s+(\S.*)$/;

/**
 * A message's trailers — "Changelog:", "Co-Authored-By:" — split from the
 * prose above them. They are data about the commit rather than part of what
 * it says, and set apart they stop reading as a last paragraph of the
 * message. Only a final paragraph made wholly of them counts, as git has it.
 */
export function splitTrailers(body: string): { text: string; trailers: Trailer[] } {
  const paragraphs = body.replace(/\r\n?/g, "\n").trim().split(/\n[ \t]*\n/);
  const lines = (paragraphs[paragraphs.length - 1] ?? "").split("\n");
  const trailers: Trailer[] = [];
  for (const line of lines) {
    const match = TRAILER_LINE.exec(line);
    if (match) {
      trailers.push({ key: match[1], value: match[2].trim() });
    } else if (/^\s+\S/.test(line) && trailers.length > 0) {
      // A trailer's value carried onto an indented line.
      const last = trailers[trailers.length - 1];
      last.value = `${last.value} ${line.trim()}`;
    } else {
      return { text: body.trim(), trailers: [] };
    }
  }
  if (trailers.length === 0) return { text: body.trim(), trailers: [] };
  return { text: paragraphs.slice(0, -1).join("\n\n").trim(), trailers };
}

/** "Claude <noreply@anthropic.com>" shown as "Claude": the address is for git. */
export function trailerValue(value: string): string {
  return value.replace(/\s*<[^<>\s]+@[^<>\s]+>$/, "") || value;
}

/**
 * "12 Oct at 14:05", with the year only when it is not this one. The list
 * gives a time under the day's heading; a commit's own page names both.
 */
export function commitDate(epochMillis: number, now = new Date()): string {
  const when = new Date(epochMillis);
  const day = `${when.getDate()} ${months[when.getMonth()]}`;
  const year = when.getFullYear() === now.getFullYear() ? "" : ` ${when.getFullYear()}`;
  return `${day}${year} at ${clockTime(epochMillis)}`;
}

const LIST_ITEM = /^\s*([-*•+]|\d+[.)])\s/;
// A trailer such as "Changelog:" or "Co-Authored-By:" sits on its own line.
const TRAILER = /^[A-Za-z][\w-]*:\s/;

/**
 * A commit body re-flowed for a phone.
 *
 * Messages are wrapped by hand at about 72 columns. On a screen narrower than
 * that, each of those lines wraps again, and a paragraph turns into long and
 * short lines in turn. Lines within a paragraph are joined so the phone does
 * the wrapping, as are the indented continuations of a list item. Blank
 * lines, new list items, indented blocks and trailers keep the breaks they
 * were written with.
 */
export function unwrapMessage(body: string): string {
  const out: string[] = [];
  for (const line of body.replace(/\r\n?/g, "\n").split("\n")) {
    const previous = out[out.length - 1];
    const indented = /^\s/.test(line);
    const joinable =
      previous !== undefined && previous.trim() !== "" && line.trim() !== "" &&
      !LIST_ITEM.test(line) && !TRAILER.test(line) &&
      (indented
        ? LIST_ITEM.test(previous)
        : !/^\s/.test(previous) || LIST_ITEM.test(previous));
    if (joinable) out[out.length - 1] = `${previous.trimEnd()} ${line.trim()}`;
    else out.push(line.trimEnd());
  }
  return out.join("\n").trim();
}

/** A filter typed on the Commits tab matches a subject, an author or a hash. */
export function commitMatches(commit: WorkspaceCommit, needle: string): boolean {
  if (!needle) return true;
  return commit.subject.toLowerCase().includes(needle) ||
    commit.author.toLowerCase().includes(needle) ||
    commit.hash.startsWith(needle);
}

/**
 * Pages appended to the ones already shown. A commit made between two pages
 * pushes everything down by one, so the next page can repeat the last commit
 * of the one before; it is shown once.
 */
export function appendPage(shown: WorkspaceCommit[], page: WorkspaceCommit[]): WorkspaceCommit[] {
  const seen = new Set(shown.map((commit) => commit.hash));
  return [...shown, ...page.filter((commit) => !seen.has(commit.hash))];
}
