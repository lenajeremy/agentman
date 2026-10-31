// "While you were away" (Views/AwaySummary.swift): a panel on the user's
// return with each agent that did something, in a line or two. Clicking an
// agent opens it; Done, Escape or a click outside closes it.

import { useEffect } from "react";
import { Sparkles, X } from "lucide-react";
import { openAgent } from "../nav";
import { askAgentman, useAgentmanOn } from "../shell/agentman";
import { useAppActive } from "../shell/native";
import { AgentMark, Icon } from "../ui";
import { closeAway, useAwayDigest, type AwayAgent, type AwayDigest } from "./away";
import "../styles/away.css";

/** "45 min away", "1 h 20 min away", "3 h away". */
export function awayDuration(ms: number): string {
  const minutes = Math.max(1, Math.floor(ms / 60_000));
  if (minutes < 60) return `${minutes} min away`;
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  return rest === 0 ? `${hours} h away` : `${hours} h ${rest} min away`;
}

function subtitle(d: AwayDigest): string {
  const parts = [awayDuration(d.until - d.since)];
  parts.push(d.agents.length === 1 ? "1 agent worked" : `${d.agents.length} agents worked`);
  const approved = d.agents.reduce((n, a) => n + a.approved, 0);
  if (approved > 0) parts.push(`Agentman approved ${approved}`);
  const waiting = d.agents.filter((a) => a.waiting).length;
  if (waiting > 0) parts.push(waiting === 1 ? "1 needs you" : `${waiting} need you`);
  return parts.join(" · ");
}

export function AwaySummary() {
  const digest = useAwayDigest();
  const keySet = useAgentmanOn();
  const active = useAppActive();
  useEffect(() => {
    if (!digest) return;
    const keys = (e: KeyboardEvent) => {
      if (e.key === "Escape" || e.key === "Enter") {
        e.preventDefault();
        e.stopPropagation();
        closeAway();
      }
    };
    window.addEventListener("keydown", keys, true);
    return () => window.removeEventListener("keydown", keys, true);
  }, [digest]);
  if (!digest) return null;
  const time = new Date(digest.since).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  return (
    <div className="away-backdrop" onClick={closeAway}>
      <div className="away-panel" role="dialog" aria-label="While you were away" onClick={(e) => e.stopPropagation()}>
        <div className="away-head">
          <div className="away-titles">
            <div className="away-title">While you were away</div>
            <div className="away-subtitle">{subtitle(digest)}</div>
          </div>
          <button className="away-close" title="Close" aria-label="Close" onClick={closeAway}>
            <Icon icon={X} sf="xmark" size={10} weight="bold" />
          </button>
        </div>
        <div className="away-rule" />
        <div className="away-rows">
          {digest.agents.map((agent) => (
            <AwayRow key={agent.id} agent={agent} open={() => {
              openAgent(agent.id);
              closeAway();
            }} />
          ))}
        </div>
        <div className="away-rule" />
        <div className="away-foot">
          {keySet && (
            <button
              className="away-ask"
              title="Agentman reads the agents' work and writes you a summary"
              onClick={() => {
                askAgentman(`Catch me up: what did my agents do since ${time}, and what needs me?`);
                closeAway();
              }}
            >
              <Icon icon={Sparkles} sf="sparkles" size={12} />
              Ask Agentman for more
            </button>
          )}
          <span style={{ flex: 1 }} />
          <button className={`away-done ${active ? "key" : ""}`} onClick={closeAway}>Done</button>
        </div>
      </div>
    </div>
  );
}

function AwayRow({ agent, open }: { agent: AwayAgent; open: () => void }) {
  const stats: string[] = [];
  if (agent.finished > 0) stats.push(agent.finished === 1 ? "1 turn finished" : `${agent.finished} turns finished`);
  if (agent.approved > 0) stats.push(agent.approved === 1 ? "1 request approved by Agentman" : `${agent.approved} requests approved by Agentman`);
  return (
    <button className="away-row" title={`Open ${agent.name}`} onClick={open}>
      <span className="away-mark"><AgentMark kind={agent.kind} size={18} /></span>
      <span className="away-body">
        <span className="away-line">
          <span className="away-name">{agent.name}</span>
          <span className="away-folder">{agent.folder.replace(/\/+$/, "").split("/").pop()}</span>
          <span style={{ flex: 1, minWidth: 6 }} />
          {agent.waiting && <span className="away-needs">Needs you</span>}
        </span>
        {agent.waiting && <span className="away-waiting">{agent.waiting}</span>}
        {agent.summary && <span className="away-summary">{agent.summary}</span>}
        {stats.length > 0 && <span className="away-stats">{stats.join(" · ")}</span>}
      </span>
    </button>
  );
}
