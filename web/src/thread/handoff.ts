// Moving a session between Agentman and its own terminal (AgentThreadView's
// handoffItem, move and TakeOverBar): Open in Terminal for one Agentman runs
// itself, Take Over for one in a terminal Agentman opened. Only between
// turns, as the daemon refuses mid-turn; Take Over asks first, since it
// closes a terminal.

import { useSyncExternalStore } from "react";
import type { Session } from "@shared/protocol";
import { daemon } from "../daemon";
import { openAgentTerminal } from "../panel/actions";
import { getStore } from "../store";
import { confirmSheet } from "./menu";
import { setActionError } from "./state";

let moving: ReadonlySet<string> = new Set();
/** Moved into a terminal, not there yet: its terminal shows once it is. */
const showWhenReady = new Set<string>();
const listeners = new Set<() => void>();

function setMoving(id: string, on: boolean) {
  const next = new Set(moving);
  if (on) next.add(id);
  else next.delete(id);
  moving = next;
  listeners.forEach((l) => l());
}

export function useMoving(id: string): boolean {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => moving.has(id),
  );
}

const working = (s: Session, isMoving: boolean) => s.state === "busy" || !!s.question || isMoving;

/** The "…" menu's entry that moves the session, if it can move. */
export function handoffItem(s: Session, isMoving: boolean): { title: string; enabled: boolean; confirms: boolean } | null {
  const busy = working(s, isMoving);
  if (s.handoff === "terminal") return { title: busy ? "Open in Terminal After This Turn" : "Open in Terminal", enabled: !busy, confirms: false };
  if (s.handoff === "agentman") return { title: busy ? "Take Over After This Turn" : "Take Over from Terminal…", enabled: !busy, confirms: true };
  return null;
}

/** A session you can only watch that Agentman can take over from its
 *  terminal, so it can be messaged here. */
export function offersTakeOver(s: Session): boolean {
  return (!s.inject || s.inject === "none") && s.state !== "ended" && s.handoff === "agentman";
}

/** Moves the session to the other side of its handoff. Into a terminal,
 *  that terminal is shown once the session is in it. */
export async function moveSession(s: Session) {
  if (moving.has(s.id) || !s.handoff) return;
  const to = s.handoff;
  setMoving(s.id, true);
  try {
    const reply = await daemon.request({ type: "handoff_session", sessionId: s.id }, 60_000);
    if (reply.type === "error") {
      setActionError(s.id, (reply.error ?? "It couldn't be moved.").replace(/^(daemon|source): /, ""));
      return;
    }
    setActionError(s.id, null);
    if (to === "terminal") {
      const now = getStore().sessions.find((x) => x.id === s.id);
      if (now?.inject === "tmux") openAgentTerminal(now);
      else showWhenReady.add(s.id);
    }
  } catch (err) {
    setActionError(s.id, err instanceof Error ? err.message : String(err));
  } finally {
    setMoving(s.id, false);
  }
}

/** Take Over asks first: its terminal closes. */
export async function confirmTakeOver(s: Session) {
  const answer = await confirmSheet("Take this session over from its terminal?", "Its terminal closes, and the conversation carries on here.", ["Take Over", "Cancel"]);
  if (answer === "Take Over") await moveSession(s);
}

/** Runs the menu entry: Take Over asks first, Open in Terminal doesn't. */
export function runHandoff(s: Session, item: { confirms: boolean }) {
  void (item.confirms ? confirmTakeOver(s) : moveSession(s));
}

daemon.on((event) => {
  if (event.type !== "session_update" || !event.session) return;
  const s = event.session;
  if (showWhenReady.has(s.id) && s.inject === "tmux") {
    showWhenReady.delete(s.id);
    openAgentTerminal(s);
  }
});

/** What the title says about a thread that can't be talked to: ended, or
 *  only watched. */
export function stateLabel(s: Session): { text: string; help: string } | null {
  if (s.state === "ended") return { text: "Ended", help: "This session has ended. Its conversation is here to read." };
  if (!s.inject || s.inject === "none") return { text: "Watching", help: "Started outside Agentman: you can follow it here, and message it once it runs through am." };
  return null;
}
