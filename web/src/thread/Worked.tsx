// How long the agent took (AgentThreadView.swift WorkedLine and
// LatestTurnLine): a quiet line between one turn and the next, and under
// the conversation the latest turn's, counting while the agent works.

import { useEffect, useMemo, useState } from "react";
import type { Message, Session } from "@shared/protocol";
import { latestTurn, turnSeconds, workDuration } from "@shared/toolgroups";
import { latestSteps } from "./items";
import { Loader } from "./Loader";

const stepCount = (steps: number) => (steps === 1 ? "1 step" : `${steps} steps`);

/** How long the agent took and how many steps, in one small line under its
 *  work; or, while it works, how long so far. */
export function WorkedLine({ seconds, steps = 0, live = false }: { seconds: number; steps?: number; live?: boolean }) {
  const text = live ? "Working for " + workDuration(seconds) : workDuration(seconds) + (steps > 0 ? " · " + stepCount(steps) : "");
  const spoken = live ? text : "Worked for " + workDuration(seconds) + (steps > 0 ? ", " + stepCount(steps) : "");
  return (
    <div className="worked-line" role="note" aria-label={spoken}>
      {live && <Loader size={9} />}
      <span className="worked-text tabular" aria-hidden>
        {text}
      </span>
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
  const steps = useMemo(() => latestSteps(messages), [messages]);
  if (start) return <WorkedLine seconds={Math.max(0, Math.floor((now - start.ts) / 1000))} live />;
  if (session.state === "waiting_input" || session.question) return null;
  const seconds = turnSeconds(turn);
  return seconds === null ? null : <WorkedLine seconds={seconds} steps={steps} />;
}
