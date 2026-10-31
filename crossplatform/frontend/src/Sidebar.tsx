// The sidebar, as Views/Sidebar.swift draws it: the app's name, search, the
// agents as rows, Agentman's card and the connection at the foot.

import { useMemo, useState } from "react";
import { ChevronRight, Contrast, ListFilter, PanelLeft, Search, SquarePen } from "lucide-react";
import type { Session } from "@shared/protocol";
import { folderLabel, initials, needsYou, since } from "./labels";
import { useStatus, useStore } from "./store";
import { AgentMark, AppIcon, ChromeButton, Icon, Spacer } from "./ui";

function AgentRow({ session, branch, selected, onSelect }: { session: Session; branch?: string; selected: boolean; onSelect: () => void }) {
  const waiting = needsYou(session);
  const ended = session.state === "ended";
  const third = waiting || !!branch;
  return (
    <button className={`agent-row ${selected ? "selected" : ""}`} onClick={onSelect}>
      <div className="agent-line1">
        <span className="badge">{initials(session.cwd)}</span>
        <span className="folder">{folderLabel(session.cwd)}</span>
        <Spacer min={4} />
        {waiting ? (
          <span className="status needs"><span className="needs-dot" />Needs you</span>
        ) : session.state === "busy" ? (
          <span className="status working"><span className="loader" />Working</span>
        ) : ended ? (
          <span className="status">Ended</span>
        ) : (
          <span className="status tabular">{since(session.lastActivityAt)}</span>
        )}
      </div>
      <div className="agent-line2">
        <span className={`name ${waiting ? "waiting" : ""} ${ended ? "ended" : ""}`}>{session.name || session.kind}</span>
        {!third && <AgentMark kind={session.kind} size={14} />}
      </div>
      {third && (
        <div className="agent-line3">
          <span className={waiting ? "question" : "branch"}>{waiting ? session.question?.prompt ?? "Waiting for you" : branch}</span>
          <Spacer />
          <AgentMark kind={session.kind} size={14} />
        </div>
      )}
    </button>
  );
}

export function Sidebar({ selected, onSelect, platform }: { selected?: string; onSelect: (id: string) => void; platform: string }) {
  const { sessions, branches, version } = useStore();
  const { status, detail } = useStatus();
  const [query, setQuery] = useState("");

  const { working, rest } = useMemo(() => {
    const q = query.trim().toLowerCase();
    const shown = sessions.filter((s) => !q || `${s.name} ${s.cwd} ${branches[s.cwd] ?? ""}`.toLowerCase().includes(q));
    const byRecent = [...shown].sort((a, b) => Number(needsYou(b)) - Number(needsYou(a)) || b.lastActivityAt - a.lastActivityAt);
    return { working: byRecent.filter((s) => s.state === "busy"), rest: byRecent.filter((s) => s.state !== "busy") };
  }, [sessions, branches, query]);

  const waiting = sessions.filter(needsYou).length;
  const busy = sessions.filter((s) => s.state === "busy").length;
  const summary = waiting ? (waiting === 1 ? "1 agent needs you" : `${waiting} agents need you`) : busy ? `${busy} agent${busy === 1 ? "" : "s"} working` : "All quiet";

  const row = (s: Session) => <AgentRow key={s.id} session={s} branch={branches[s.cwd]} selected={s.id === selected} onSelect={() => onSelect(s.id)} />;

  return (
    <nav className="sidebar">
      <div className="sidebar-header drag" style={{ paddingLeft: platform === "darwin" ? 82 : 16 }}>
        <AppIcon size={20} />
        <span className="app-name">Agentman</span>
        <Spacer />
        <span className="no-drag"><ChromeButton icon={PanelLeft} title="Hide Sidebar" /></span>
      </div>
      <div className="search-row">
        <Icon icon={Search} size={13} />
        <input id="search" placeholder="Search agents" value={query} onChange={(e) => setQuery(e.target.value)} />
        <span className="filter"><Icon icon={ListFilter} size={13} /></span>
        <Icon icon={SquarePen} size={13} />
      </div>
      <div className="agent-list">
        {working.length > 0 && (
          <>
            <div className="section-head">Working <span className="count">{working.length}</span><Icon icon={ChevronRight} size={9} weight="semibold" className="open" /></div>
            {working.map(row)}
            <div className="section-rule" />
          </>
        )}
        {rest.map(row)}
      </div>
      <div className="agentman-card-wrap">
        <button className="agentman-card">
          <AppIcon size={19} />
          <span className="agentman-text">
            <span className="agentman-title">Agentman</span>
            <span className={`agentman-summary ${waiting ? "needs" : ""}`}>{summary}</span>
          </span>
        </button>
      </div>
      <footer className="sidebar-footer">
        <span className={`conn ${status}`} />
        <span className="conn-text">
          {status === "connected" ? `${platform === "darwin" ? "Mac connected" : "Connected"}${version ? ` · am ${version}` : ""}` : status === "connecting" ? "Connecting to am serve…" : `${detail}. Run am serve.`}
        </span>
        <Spacer />
        <span className="appearance"><Icon icon={Contrast} size={13} /></span>
      </footer>
    </nav>
  );
}
