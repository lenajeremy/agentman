// How many files an agent has changed since the last commit, for the
// thread's "N files changed" bar: read when the thread opens and after each
// turn (AgentThreadView.swift).

import { useEffect, useState } from "react";
import type { Session } from "@shared/protocol";
import { daemon } from "../daemon";

export function useChangedCount(session: Session): number {
  const [count, setCount] = useState(0);
  const busy = session.state === "busy";
  useEffect(() => {
    if (busy) return;
    let cancelled = false;
    daemon
      .request({ type: "list_changes", sessionId: session.id })
      .then((reply) => {
        if (!cancelled && reply.workspace) setCount(reply.workspace.changes?.length ?? 0);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [session.id, busy]);
  return count;
}
