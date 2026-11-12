// An agent in two lines, as the sidebar and the editor's agent panel show it
// (Sidebar.swift's AgentRow): its state as a dot, its title and how long
// since it last did anything; under the title, the agent's logo, then its
// folder and branch, or what it asks while it needs you.

import type { ReactNode } from "react";
import type { Session } from "@shared/protocol";
import { folderLabel, since } from "../labels";
import { AgentMark, Spacer } from "../ui";
import { Loader } from "./Loader";
import { filterState } from "./sections";
import "../styles/agentrow.css";

/** A session as am serve sends it to this app: with the agent's own name for
 *  one the user renamed. */
const ownName = (s: Session) => (s as Session & { originalName?: string }).originalName;

/** title: what stands for its name, as the field it is renamed in. */
export function AgentRow({ session, branch, archived = false, title }: { session: Session; branch?: string; archived?: boolean; title?: ReactNode }) {
  const waiting = filterState(session) === "waiting_input";
  const servers = session.servers ?? [];
  const folder = folderLabel(session.cwd);
  const detail = session.question?.title || session.question?.prompt || "Waiting for your answer";
  const own = ownName(session);
  return (
    <>
      <span className="agent-line1">
        <StateDot state={waiting ? "needs" : session.state === "busy" ? "working" : "idle"} />
        {title ?? (
          <span className={`agent-name ${session.state === "ended" || archived ? "ended" : ""}`} title={own ? `Renamed from “${own}”` : session.name}>
            {session.name || session.kind}
          </span>
        )}
        <Spacer min={4} />
        {session.state === "ended" ? (
          <span className="agent-when">Ended</span>
        ) : session.lastActivityAt ? (
          <span className="agent-when tabular">{since(session.lastActivityAt)}</span>
        ) : null}
      </span>
      <span className="agent-line2">
        <AgentMark kind={session.kind} size={12} muted={archived} />
        {waiting ? (
          <span className="agent-place needs">{folder} · {detail}</span>
        ) : (
          <span className="agent-place">
            {folder}
            {branch ? ` · ${branch}` : ""}
            {servers.length > 0 && <> · <span className="agent-ports" title="Dev servers it is running">{servers.map((s) => `:${s.port}`).join(" ")}</span></>}
          </span>
        )}
      </span>
    </>
  );
}

/** An agent's state, before its title, in the same colours everywhere
 *  (StateDot): orange when it needs you, the loader turning while it works,
 *  a grey ring while it waits for something to do. */
export function StateDot({ state }: { state: "needs" | "working" | "idle" }) {
  return (
    <span className="state-dot" aria-label={state === "needs" ? "Needs you" : undefined}>
      {state === "working" ? <Loader size={9} /> : <span className={state === "needs" ? "dot-needs" : "dot-idle"} />}
    </span>
  );
}
