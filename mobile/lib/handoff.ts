/**
 * Moving a live session between Agentman and a terminal on the Mac
 * (Session.handoff): Open in terminal for a Kiro chat Agentman runs over ACP,
 * Take over for one in a pane Agentman started.
 */

export type Handoff = "terminal" | "agentman";

export interface HandoffItem {
  to: Handoff;
  label: string;
  icon: "terminal" | "log-in";
  /** Mid-turn, or waiting on an answer, the Mac refuses: the row is dimmed
   *  until the turn ends, as it is while a move is under way or the Mac is
   *  out of reach. */
  disabled: boolean;
}

/** The session menu's row that moves the session, or null when it cannot
 *  move (a value from a newer Mac included). */
export function handoffItem(
  session: { handoff?: string; state: string; question?: unknown },
  moving: boolean,
  online: boolean,
): HandoffItem | null {
  if (session.handoff !== "terminal" && session.handoff !== "agentman") return null;
  const toTerminal = session.handoff === "terminal";
  const working = session.state === "busy" || Boolean(session.question);
  return {
    to: session.handoff,
    label: toTerminal ? "Open in terminal" : "Take over",
    icon: toTerminal ? "terminal" : "log-in",
    disabled: working || moving || !online,
  };
}

/** A refused move, in words worth reading on a phone. */
export function handoffError(error: string): string {
  if (error.includes("unsupported request")) {
    return "Update agentman on your Mac to move sessions from here.";
  }
  return error.replace(/^(daemon|source): /, "");
}
