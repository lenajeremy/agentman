import { useEffect, useMemo, useState } from "react";
import type { Session, SessionState } from "@shared/protocol";
import { Editor } from "./Editor";
import { platform } from "./host";
import { useStatus, useStore } from "./store";
import { Terminal } from "./Terminal";
import { Thread } from "./Thread";

type Tab = "thread" | "terminal" | "editor";

const stateLabel: Record<SessionState, string> = {
  busy: "Working",
  idle: "Idle",
  waiting_input: "Needs you",
  ended: "Ended",
};

function folderName(cwd: string) {
  return cwd.replace(/\/$/, "").split("/").pop() || cwd;
}

function Sidebar({ sessions, selected, onSelect }: { sessions: Session[]; selected?: string; onSelect: (id: string) => void }) {
  const groups = useMemo(() => {
    const byFolder = new Map<string, Session[]>();
    for (const s of [...sessions].sort((a, b) => b.lastActivityAt - a.lastActivityAt)) {
      const list = byFolder.get(s.cwd) ?? [];
      list.push(s);
      byFolder.set(s.cwd, list);
    }
    return [...byFolder.entries()];
  }, [sessions]);

  return (
    <nav className="sidebar">
      <div className="sidebar-head drag">Agents</div>
      <div className="sidebar-list">
        {groups.length === 0 && <div className="empty">No agents running</div>}
        {groups.map(([cwd, list]) => (
          <section key={cwd} className="group">
            <div className="group-name" title={cwd}>{folderName(cwd)}</div>
            {list.map((s) => (
              <button key={s.id} className={`agent ${s.id === selected ? "selected" : ""}`} onClick={() => onSelect(s.id)}>
                <span className={`dot ${s.state}`} title={stateLabel[s.state]} />
                <span className="agent-name">{s.name || s.kind}</span>
                <span className="agent-kind">{s.kind}</span>
              </button>
            ))}
          </section>
        ))}
      </div>
    </nav>
  );
}

export default function App() {
  const { sessions, version } = useStore();
  const { status, detail } = useStatus();
  const [selectedId, setSelectedId] = useState<string>();
  const [tab, setTab] = useState<Tab>(() => {
    const asked = new URLSearchParams(location.search).get("tab");
    return asked === "terminal" || asked === "editor" ? asked : "thread";
  });
  const [os, setOs] = useState("");

  useEffect(() => {
    void platform().then(setOs);
  }, []);

  const selected = sessions.find((s) => s.id === selectedId) ?? sessions[0];

  return (
    <div className="app">
      <Sidebar sessions={sessions} selected={selected?.id} onSelect={setSelectedId} />
      <main className="main">
        <header className="main-head drag">
          {selected ? (
            <>
              <div className="title">
                <span className={`dot ${selected.state}`} />
                <span className="title-name">{selected.name || selected.kind}</span>
                <span className="title-folder">{selected.cwd}</span>
              </div>
              <div className="tabs no-drag" role="tablist">
                {(["thread", "terminal", "editor"] as Tab[]).map((t) => (
                  <button key={t} role="tab" aria-selected={tab === t} className={tab === t ? "on" : ""} onClick={() => setTab(t)}>
                    {t === "thread" ? "Conversation" : t === "terminal" ? "Terminal" : "Editor"}
                  </button>
                ))}
              </div>
            </>
          ) : (
            <div className="title" />
          )}
        </header>
        <div className="main-body">
          {!selected && <div className="empty big">{status === "connected" ? "No agents yet" : "Connecting to am serve…"}</div>}
          {selected && tab === "thread" && <Thread session={selected} />}
          {selected && tab === "terminal" && <Terminal target={{ session: selected.id }} />}
          {selected && tab === "editor" && <Editor session={selected} />}
        </div>
        <footer className="status">
          <span className={`conn ${status}`} />
          {status === "connected" ? `Connected · am ${version ?? "?"}` : status === "connecting" ? "Connecting…" : `Offline · ${detail}`}
          <span className="spacer" />
          <span className="faint">{os}</span>
        </footer>
      </main>
    </div>
  );
}
