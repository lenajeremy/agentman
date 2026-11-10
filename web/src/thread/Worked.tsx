// How long the agent took (AgentThreadView.swift WorkedLine and
// LatestTurnLine): a quiet line between one turn and the next, and under
// the conversation the latest turn's, counting while the agent works.

import { useEffect, useMemo, useState } from "react";
import type { Message, Session } from "@shared/protocol";
import { latestTurn, turnSeconds, workDuration } from "@shared/toolgroups";
import { Loader } from "./Loader";

export function WorkedLine({ text, live = false }: { text: string; live?: boolean }) {
  return (
    <div className="worked-line" role="note" aria-label={text}>
      {live && <Loader size={9} />}
      <span className="worked-text tabular" aria-hidden>
        {text}
      </span>
      <span className="worked-rule" />
    </div>
  );
}

/** The latest turn's line: counting while the agent works, and what it came
 *  to once it stops. Nothing while it waits on the user. */
export function LatestTurnLine({ session, messages }: { session: Session; messages: readonly Message[] }) {
  const turn = useMemo(() => latestTurn(messages), [messages]);
  const start = session.state === "busy" ? turn.start : null;
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!start) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [start]);
  if (start) return <WorkedLine text={"Working for " + workDuration(Math.max(0, Math.floor((now - start.ts) / 1000)))} live />;
  if (session.state === "waiting_input" || session.question) return null;
  const seconds = turnSeconds(turn);
  return seconds === null ? null : <WorkedLine text={"Worked for " + workDuration(seconds)} />;
}
