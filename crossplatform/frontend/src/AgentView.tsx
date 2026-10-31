// An agent in the detail area (AgentThreadView.swift's layout): its header,
// the conversation and message box with the workspace card beside them, the
// terminal under them while the folder has a shell, and the right panel at
// the window's edge, the whole height, resized from its leading edge.

import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { Session } from "@shared/protocol";
import { AgentHeader } from "./AgentHeader";
import { Composer } from "./Composer";
import { openEditor } from "./host";
import { usePref } from "./prefs";
import { RightPanel } from "./RightPanel";
import { FolderTerminal } from "./Terminal";
import { Thread } from "./Thread";
import { WorkspaceCard } from "./WorkspaceCard";
import { useCardTab } from "./workspace/cardTab";
import { openTerminal } from "./panel/actions";
import { PaneResizeHandle, PanelDivider } from "./panel/Dividers";
import {
  BOTTOM_HEIGHT_KEY, BOTTOM_KEY, CARD_WIDTH, clampWidth, closePanel, DEFAULT_WIDTH, OPEN_KEY, showPanel, TAB_KEY,
  THREAD_ROOM, WIDTH_KEY, type PanelTab,
} from "./panel/state";
import { shellCommand, useShells } from "./terminal/shells";
import { useFolderExists } from "./workspace/folder";
import "./styles/panel.css";

// ?tab=terminal keeps the terminal under the conversation, for comparisons.
const query = new URLSearchParams(location.search);

export function AgentView({ session, platform }: { session: Session; platform: string }) {
  const [panelOpen] = usePref(OPEN_KEY, false);
  const [panelTab] = usePref<PanelTab>(TAB_KEY, "diff");
  const [panelWidth, setPanelWidth] = usePref(WIDTH_KEY, DEFAULT_WIDTH);
  const [bottomKept, setBottomKept] = usePref(BOTTOM_KEY, query.get("tab") === "terminal");
  const [bottomHeight, setBottomHeight] = usePref(BOTTOM_HEIGHT_KEY, 260);
  const [maximised, setMaximised] = useState(false);
  const [showFiles, setShowFiles] = useState(true);
  const filesTab = useCardTab();
  const [sent, setSent] = useState(0);
  const shells = useShells(session.cwd);
  const folderExists = useFolderExists(session.id, session.cwd);

  // ?tab=terminal: a shell open in the folder, as the Mac's
  // AGENTMAN_TEST_TERMINAL opens one, so the terminal has one to show.
  useEffect(() => {
    if (query.get("tab") === "terminal") shellCommand(session.cwd, { type: "ensure" });
  }, [session.cwd]);

  // The detail area's size, as the Mac's GeometryReader reads it.
  const box = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 1200, height: 900 });
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    const read = () => setSize({ width: el.clientWidth, height: el.clientHeight });
    read();
    const observer = new ResizeObserver(read);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  // The card shows again for each agent, as the Mac's view state starts, and
  // when something opens it on a list.
  useEffect(() => setShowFiles(true), [session.id, filesTab.n]);

  const width = clampWidth(panelWidth, size.width);
  // The card goes while the panel leaves too little room for it beside the conversation.
  const cardFits = !panelOpen || size.width - width >= THREAD_ROOM + CARD_WIDTH + 12;
  const bottomShows = bottomKept && folderExists && !shells.problem && shells.shells.length > 0;
  const tallest = Math.max(size.height - 260, 160);
  const inPanelTab = panelOpen && panelTab === "terminal";

  const closeThePanel = () => {
    closePanel();
    setMaximised(false);
  };

  return (
    <div className="agent-view" ref={box}>
      {!(panelOpen && maximised) && (
        <div className="agent-main">
          <AgentHeader
            session={session}
            terminal={bottomShows || inPanelTab}
            panel={panelOpen}
            card={showFiles}
            onTerminal={() => {
              if (bottomShows) setBottomKept(false);
              else if (inPanelTab) closeThePanel();
              else openTerminal(session.cwd);
            }}
            onPanel={() => (panelOpen ? closeThePanel() : showPanel("diff"))}
            onCard={() => setShowFiles(!showFiles)}
          />
          <div className="detail-body">
            <div className="conversation">
              <div className="top-fade" />
              <Thread session={session} stickToEnd={sent} />
              <Composer session={session} onSent={() => setSent((n) => n + 1)} />
            </div>
            {showFiles && cardFits && (
              <WorkspaceCard
                session={session}
                platform={platform}
                tab={filesTab}
                folderGone={!folderExists}
                onEditor={() => void openEditor({ folder: session.cwd, sessionId: session.id })}
                onTerminal={() => openTerminal(session.cwd)}
              />
            )}
          </div>
          {bottomShows && (
            <>
              <PanelDivider height={bottomHeight} maximum={tallest} onChange={setBottomHeight} />
              <div className="bottom-terminal" style={{ height: Math.min(Math.max(bottomHeight, 120), tallest) }}>
                <FolderTerminal folder={session.cwd} hide={() => setBottomKept(false)} move="toSide" />
              </div>
            </>
          )}
        </div>
      )}
      {panelOpen && (
        <div className="right-panel-slot" style={maximised ? { flex: 1 } : { width }}>
          <RightPanel session={session} maximised={maximised} onMaximise={() => setMaximised(!maximised)} onClose={closeThePanel} />
          {!maximised && (
            <PaneResizeHandle
              width={width}
              onChange={(w) => setPanelWidth(clampWidth(w, size.width))}
              reset={() => setPanelWidth(DEFAULT_WIDTH)}
            />
          )}
        </div>
      )}
    </div>
  );
}
