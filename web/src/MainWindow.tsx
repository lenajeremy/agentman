// The main window (Views/ContentView.swift): the sidebar, dragged to a width
// of the user's or hidden (⌃⌘S); then the detail area — Agentman, New
// Agent, an agent, or "This agent has ended" — and the overlays over
// everything (toasts, palette, away summary, lightbox).

import { ConnectionBanner } from "./shell/ConnectionBanner";
import { useEffect, useState, type CSSProperties, type PointerEvent as ReactPointerEvent } from "react";
import { Moon, PanelLeft } from "lucide-react";
import type { Session } from "@shared/protocol";
import { AgentView } from "./AgentView";
import { setBadge } from "./host";
import { BoardView } from "./board/BoardView";
import { useManagerSummary } from "./manager/summary";
import { NewAgentView } from "./NewAgentView";
import { Overlays } from "./Overlays";
import { clampSidebar, Sidebar, SIDEBAR_DEFAULT } from "./Sidebar";
import { openAgentman, useRoute } from "./nav";
import { usePref } from "./prefs";
import { startShellCommands } from "./shell/commands";
import { useFullscreen } from "./shell/native";
import { useShell } from "./shell/state";
import { sessionsLoaded, useStore } from "./store";
import { ChromeButton, Icon } from "./ui";
import "./notify/agents";
import "./styles/shell.css";



function useWindowWidth(): number {
  const [width, setWidth] = useState(window.innerWidth);
  useEffect(() => {
    const resize = () => setWidth(window.innerWidth);
    window.addEventListener("resize", resize);
    return () => window.removeEventListener("resize", resize);
  }, []);
  return width;
}

export function MainWindow({ platform }: { platform: string }) {
  const route = useRoute();
  const { sessions } = useStore();
  const { pastSessions, foundSessions } = useShell();
  const [sidebarWidth, setSidebarWidth] = usePref("sidebarWidth", SIDEBAR_DEFAULT);
  const [sidebarHidden, setSidebarHidden] = usePref("sidebarHidden", false);
  // The sidebar steps aside on the Board, whose header has its search and
  // folders; brought out over it for now, it goes again with the next visit
  // (AgentsStore.boardShowsSidebar).
  const onBoard = route.kind === "agentman";
  const [boardShowsSidebar, setBoardShowsSidebar] = useState(false);
  useEffect(() => {
    if (!onBoard) setBoardShowsSidebar(false);
  }, [onBoard]);
  const sidebarGone = sidebarHidden || (onBoard && !boardShowsSidebar);
  const fullscreen = useFullscreen();
  const windowWidth = useWindowWidth();
  useEffect(() => startShellCommands(), []);
  // The dock icon's badge: Agentman's news not read yet.
  const { unread } = useManagerSummary();
  useEffect(() => {
    void setBadge(unread > 0 ? String(unread) : "").catch(() => {});
  }, [unread]);

  // Room for the window's buttons on the Mac; none in full screen, or
  // where the system draws a title bar of its own.
  const mac = platform === "darwin";
  const headerLeading = mac && !fullscreen ? 82 : 16;
  const width = clampSidebar(sidebarWidth, windowWidth);
  const selected: Session | undefined = route.kind === "agent"
    ? sessions.find((s) => s.id === route.id) ?? pastSessions.find((s) => s.id === route.id) ?? foundSessions[route.id]
    : undefined;

  const style = { "--safe-top": `${mac && !fullscreen ? 32 : 0}px`, "--header-leading": `${headerLeading}px`, "--detail-header-leading": sidebarGone ? `${headerLeading + 34}px` : "0px" } as CSSProperties;

  return (
    <div className="app" style={style}>
      {!sidebarGone && (
        <div className="sidebar-pane">
          <Sidebar width={width} headerLeading={headerLeading} />
          <ResizeHandle width={width} onWidth={(w) => setSidebarWidth(clampSidebar(w, window.innerWidth))} />
        </div>
      )}
      <main className="detail">
        {route.kind === "agentman" && <BoardView headerLeading={sidebarGone ? headerLeading + 34 : 0} />}
        {route.kind === "new" && <NewAgentView worktree={route.worktree} />}
        {route.kind === "agent" && (selected ? <AgentView key={selected.id} session={selected} platform={platform} /> : sessionsLoaded() ? <GoneView /> : null)}
        {/* Under the page's top bar while am serve can't be reached. */}
        <ConnectionBanner />
        {/* With the sidebar hidden, the headers make room for the window's
            buttons and the one that brings it back. */}
        {sidebarGone && (
          <div className="show-sidebar no-drag" style={{ left: headerLeading }}>
            <ChromeButton icon={PanelLeft} sf="sidebar.left" title="Show the sidebar (⌃⌘S)" onClick={() => {
              setSidebarHidden(false);
              if (onBoard) setBoardShowsSidebar(true);
            }} />
          </div>
        )}
      </main>
      <Overlays />
    </div>
  );
}

/** The line between the sidebar and the detail: drag to resize, double-click
 *  for the default width (Sidebar.swift's PaneResizeHandle). */
function ResizeHandle({ width, onWidth }: { width: number; onWidth: (w: number) => void }) {
  function down(e: ReactPointerEvent<HTMLDivElement>) {
    if (e.button !== 0) return;
    const start = e.clientX;
    const from = width;
    const el = e.currentTarget;
    el.setPointerCapture(e.pointerId);
    const move = (m: PointerEvent) => onWidth(from + (m.clientX - start));
    const up = () => {
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", up);
      el.removeEventListener("pointercancel", up);
    };
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", up);
    el.addEventListener("pointercancel", up);
  }
  return <div className="pane-resize" style={{ left: width - 4.5 }} onPointerDown={down} onDoubleClick={() => onWidth(SIDEBAR_DEFAULT)} aria-hidden />;
}

/** An agent that ended while it was open. */
function GoneView() {
  return (
    <div className="gone">
      <span className="gone-icon"><Icon icon={Moon} sf="moon.zzz" size={28} /></span>
      <div className="gone-title">This agent has ended</div>
      <button className="gone-link" onClick={openAgentman}>Back to Agentman</button>
    </div>
  );
}
