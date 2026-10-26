/**
 * A system line in a thread, said quietly.
 *
 * Claude Code tells the agent when a background task ends with a sentence
 * of its own: `Background command "Build the app" completed (exit code 0)`.
 * In the feed that is a step of the work like any other, so it reads as one:
 * what ran, and how it ended.
 */

export interface Notice {
  /** What it is about: the background task's own description, or the line. */
  label: string;
  /** How it ended, when it says: "finished", "failed (exit 1)", "stopped". */
  outcome?: string;
  tone: "done" | "failed" | "info";
}

const BACKGROUND = /^Background command "(.+)" (.+?)\.?$/s;

export function noticeOf(text: string): Notice {
  const line = text.trim();
  const background = BACKGROUND.exec(line);
  if (!background) return { label: line, tone: "info" };
  const [, label, rest] = background;
  const exit = /exit code (\d+)/.exec(rest);
  if (exit && exit[1] !== "0") return { label, outcome: `failed (exit ${exit[1]})`, tone: "failed" };
  if (/fail|error/i.test(rest)) return { label, outcome: "failed", tone: "failed" };
  if (/kill|stop|cancel/i.test(rest)) return { label, outcome: "stopped", tone: "info" };
  if (/complete|finish|done/i.test(rest) || exit) return { label, outcome: "finished", tone: "done" };
  return { label, outcome: rest, tone: "info" };
}
