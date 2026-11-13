// Agentman at the side of the code (Editor/AgentPanel.swift): it opens on
// the agent working in the editor's folder; Back lists every agent and
// Agentman itself, and any of them can be read and written to from here.
// The thread and Agentman's chat are the main window's own views.

import { useEffect, useRef, useState } from "react";
import { ChevronLeft, ChevronRight, SquareArrowOutUpRight, X } from "lucide-react";
import type { Session } from "@shared/protocol";
import { Composer } from "../Composer";
import { Thread } from "../Thread";
import { AgentmanView } from "../manager/AgentmanView";
import { AgentRow } from "../shell/AgentRow";
import { useStore } from "../store";
import { AgentMark, AppIcon, Icon } from "../ui";
import { needsYou } from "../labels";
import { paths } from "./fs";
import { folderAgent, type EditorStore, type PanelRoute } from "./store";

const PANEL_MIN = 300;

export function AgentPanel({ store }: { store: EditorStore }) {
  const { sessions } = useStore();
  const route: PanelRoute = store.panelRoute ?? { kind: "agents" };

  useEffect(() => {
    if (store.panelRoute) return;
    // For screenshots and tests: the list, Agentman, or an agent.
    const shown = new URLSearchParams(location.search).get("panel");
    if (shown) {
      store.panelRoute = shown === "agents" ? { kind: "agents" } : shown === "agentman" ? { kind: "agentman" } : { kind: "agent", id: shown };
    } else {
      const session = folderAgent(store.root, sessions);
      store.panelRoute = session ? { kind: "agent", id: session.id } : { kind: "agents" };
    }
    store.changed();
  }, [store, sessions]);

  const go = (next: PanelRoute) => {
    store.panelRoute = next;
    store.changed();
  };

  let body;
  if (route.kind === "agents") {
    body = <PanelAgentList folder={store.root} sessions={sessions} choose={go} />;
  } else if (route.kind === "agentman") {
    body = <AgentmanView />;
  } else {
    const session = sessions.find((s) => s.id === route.id);
    body = session ? <PanelThread key={session.id} session={session} store={store} /> : <PanelGone back={() => go({ kind: "agents" })} />;
  }
  return (
    <div className="agent-panel">
      <PanelHeader store={store} route={route} sessions={sessions} go={go} />
      <div className="agent-panel-body">{body}</div>
    </div>
  );
}

function PanelThread({ session, store }: { session: Session; store: EditorStore }) {
  const [sent, setSent] = useState(0);
  return (
    <div className="agent-panel-thread">
      {/* "4 files changed" fills the editor with the agent's changes. */}
      <Thread session={session} stickToEnd={sent} findable={false} card={false} onShowChanges={() => store.showChanges(session.id)} />
      <Composer session={session} onSent={() => setSent((n) => n + 1)} />
    </div>
  );
}

function stateWord(session: Session) {
  switch (session.state) {
    case "waiting_input": return "Needs you";
    case "busy": return "Working";
    case "idle": return "Idle";
    case "ended": return "Ended";
    default: return "";
  }
}

function dotColour(session: Session) {
  return session.state === "waiting_input" ? "var(--needs-you)" : session.state === "busy" ? "var(--working)" : "var(--faint)";
}

/** The panel's title row, as tall as the tab strip beside it. */
function PanelHeader({ store, route, sessions, go }: { store: EditorStore; route: PanelRoute; sessions: Session[]; go: (r: PanelRoute) => void }) {
  const session = route.kind === "agent" ? sessions.find((s) => s.id === route.id) : undefined;
  return (
    <div className="panel-header">
      {route.kind !== "agents" && <HeaderButton icon={ChevronLeft} sf="chevron.left" help="All agents and Agentman" onClick={() => go({ kind: "agents" })} />}
      <div className="panel-title" style={{ paddingLeft: route.kind === "agents" ? 6 : 0 }}>
        {route.kind === "agents" && <span className="panel-title-text">Agents</span>}
        {route.kind === "agentman" && (
          <>
            <AppIcon size={18} />
            <span className="panel-title-text">Agentman</span>
          </>
        )}
        {session && (
          <span className="panel-agent" title={`${session.name} in ${session.cwd}`}>
            <AgentMark kind={session.kind} size={15} />
            <span className="panel-title-text panel-agent-name">{session.name || session.kind}</span>
            <span className="panel-dot" style={{ background: dotColour(session) }} />
            <span className="panel-state" style={{ color: session.state === "waiting_input" ? "var(--needs-you-text)" : "var(--muted)" }}>{stateWord(session)}</span>
          </span>
        )}
      </div>
      <span style={{ flex: 1, minWidth: 6 }} />
      {route.kind === "agentman" && (
        <HeaderButton icon={SquareArrowOutUpRight} sf="arrow.up.forward.app" help="Open Agentman in its window" onClick={() => openInMain("agentman://home")} />
      )}
      {route.kind === "agent" && (
        <HeaderButton icon={SquareArrowOutUpRight} sf="arrow.up.forward.app" help="Open this agent in Agentman's window" onClick={() => openInMain(`agentman://agent/${encodeURIComponent(route.id)}`)} />
      )}
      <HeaderButton icon={X} sf="xmark" help="Close the panel (⌘L)" onClick={() => store.setShowAgentPanel(false)} />
    </div>
  );
}

/** Shows the main window on a link, as openWindow(id: "main") does. */
function openInMain(link: string) {
  // The main window follows agentman:// links; the shell brings it forward.
  window.dispatchEvent(new CustomEvent("agentman:open-main", { detail: link }));
}

function HeaderButton({ icon, sf, help, onClick }: { icon: typeof X; sf: string; help: string; onClick: () => void }) {
  return (
    <button className="panel-header-button" title={help} aria-label={help} onClick={onClick}>
      <Icon icon={icon} sf={sf} size={11.5} weight="semibold" />
    </button>
  );
}

function related(cwd: string, folder: string) {
  const a = paths.normalize(cwd);
  const b = paths.normalize(folder);
  return a === b || a.startsWith(b + "/") || b.startsWith(a + "/");
}

/** Agentman, then the agents in the editor's folder, then the rest. */
function PanelAgentList({ folder, sessions, choose }: { folder: string; sessions: Session[]; choose: (r: PanelRoute) => void }) {
  const running = sessions.filter((s) => s.state !== "ended");
  const here = running.filter((s) => related(s.cwd, folder));
  const elsewhere = running.filter((s) => !related(s.cwd, folder));
  const waiting = running.filter(needsYou).length;
  const working = running.filter((s) => s.state === "busy").length;
  const summary = waiting
    ? waiting === 1 ? "1 agent needs you" : `${waiting} agents need you`
    : working
      ? working === 1 ? "1 agent working" : `${working} agents working`
      : "Ask about any of your agents";
  const row = (s: Session) => (
    <button key={s.id} className="panel-row" title={s.cwd} onClick={() => choose({ kind: "agent", id: s.id })}>
      <PanelAgentRow session={s} />
    </button>
  );
  return (
    <div className="panel-list">
      <button className="panel-agentman" onClick={() => choose({ kind: "agentman" })}>
        <AppIcon size={28} />
        <span className="panel-agentman-text">
          <span className="panel-agentman-name">Agentman</span>
          <span className="panel-agentman-summary" style={{ color: waiting ? "var(--needs-you-text)" : "var(--muted)" }}>{summary}</span>
        </span>
        <span style={{ flex: 1, minWidth: 4 }} />
        <Icon icon={ChevronRight} sf="chevron.right" size={9} weight="bold" className="panel-chevron" />
      </button>
      {here.length > 0 && (
        <>
          <div className="panel-heading">THIS FOLDER</div>
          {here.map(row)}
        </>
      )}
      {elsewhere.length > 0 && (
        <>
          <div className="panel-heading" style={{ paddingTop: here.length ? 10 : 0 }}>{here.length ? "ELSEWHERE" : "AGENTS"}</div>
          {elsewhere.map(row)}
        </>
      )}
      {running.length === 0 && <div className="panel-none">No agents are running. Ask Agentman, or start one from its window.</div>}
    </div>
  );
}

/** An agent as the sidebar shows it (Sidebar.swift's AgentRow). */
function PanelAgentRow({ session }: { session: Session }) {
  const { branches } = useStore();
  return (
    <span className="panel-agent-row">
      <AgentRow session={session} branch={branches[session.cwd]} />
    </span>
  );
}

/** An agent that ended while its thread was open in the panel. */
function PanelGone({ back }: { back: () => void }) {
  return (
    <div className="panel-gone">
      <span>This agent isn't running any more.</span>
      <button className="mac-button" onClick={back}>All Agents</button>
    </div>
  );
}

/** The edge between the editor and the panel: drag it to resize. */
export function ColumnDivider({ width, setWidth, maximum }: { width: number; setWidth: (w: number) => void; maximum: number }) {
  const start = useRef<{ x: number; width: number } | null>(null);
  return (
    <div
      className="column-divider"
      onPointerDown={(e) => {
        start.current = { x: e.clientX, width };
        e.currentTarget.setPointerCapture(e.pointerId);
      }}
      onPointerMove={(e) => {
        if (!start.current) return;
        const next = start.current.width - (e.clientX - start.current.x);
        setWidth(Math.min(Math.max(next, PANEL_MIN), Math.max(maximum, PANEL_MIN)));
      }}
      onPointerUp={() => {
        start.current = null;
      }}
    />
  );
}

export { PANEL_MIN };
