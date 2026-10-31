// The panel at the window's right edge (Views/RightPanel.swift), as T3 Code
// has one: what the agent has changed as one diff, the project's files, its
// history and its terminal, each in a tab. The open tabs are kept across
// launches; it resizes from its leading edge and can take the whole window.

import { useRef } from "react";
import { Clock, FileText, Folder, Maximize2, Minimize2, PanelRight, Plus, SquareTerminal, X } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { Session } from "@shared/protocol";
import { usePref } from "./prefs";
import { FolderTerminal } from "./Terminal";
import { menuUnder } from "./thread/menu";
import { ChromeButton } from "./ui";
import { DiffPanel } from "./panel/DiffPanel";
import { Sym } from "./panel/Sym";
import { FilesPanel } from "./panel/FilesPanel";
import { HistoryPanel } from "./panel/HistoryPanel";
import {
  dockTerminal, PANEL_TABS, showPanel, TAB_KEY, tabSymbol, tabTitle, usePanelTabs, type PanelTab,
} from "./panel/state";
import { shellCommand } from "./terminal/shells";
import "./styles/panel.css";

const TAB_ICON: Record<PanelTab, LucideIcon> = { diff: FileText, files: Folder, history: Clock, terminal: SquareTerminal };

export function RightPanel({ session, maximised, onMaximise, onClose }: {
  session: Session;
  maximised: boolean;
  onMaximise: () => void;
  onClose: () => void;
}) {
  const [tabs, setTabs] = usePanelTabs();
  const [tab, setTab] = usePref<PanelTab>(TAB_KEY, "diff");
  const shown = tabs.includes(tab) ? tab : tabs[0] ?? "diff";
  const plus = useRef<HTMLButtonElement>(null);

  const closeTab = (item: PanelTab) => {
    const rest = tabs.filter((t) => t !== item);
    if (!rest.length) {
      onClose();
      return;
    }
    setTabs(rest);
    if (tab === item) setTab(rest[0]);
  };

  return (
    <aside className="right-panel">
      <div className="panel-tabs" style={{ paddingLeft: maximised ? 12 : 10 }}>
        {tabs.map((item) => (
          <div key={item} className={`panel-tab ${item === shown ? "selected" : ""}`} onClick={() => setTab(item)} role="button" aria-label={tabTitle[item]}>
            <Sym icon={TAB_ICON[item]} sf={tabSymbol[item]} size={11} />
            <span className="panel-tab-title">{tabTitle[item]}</span>
            <button className="panel-tab-close" title={`Close ${tabTitle[item]}`} onClick={(e) => {
              e.stopPropagation();
              closeTab(item);
            }}>
              <Sym icon={X} sf="xmark" size={8} weight="bold" />
            </button>
          </div>
        ))}
        <button
          ref={plus}
          className="panel-plus"
          title="Open a panel"
          onClick={() => menuUnder(plus.current!, PANEL_TABS.map((item) => ({
            label: tabTitle[item],
            action: () => {
              if (item === "terminal") {
                shellCommand(session.cwd, { type: "ensure" });
                dockTerminal("side");
              } else {
                showPanel(item);
              }
            },
          })))}
        >
          <Sym icon={Plus} sf="plus" size={13} weight="medium" />
        </button>
        <span className="panel-tabs-spacer" />
        <ChromeButton
          icon={maximised ? Minimize2 : Maximize2}
          sf={maximised ? "arrow.down.right.and.arrow.up.left" : "arrow.up.left.and.arrow.down.right"}
          title={maximised ? "Restore the panel" : "Fill the window"}
          active={maximised}
          onClick={onMaximise}
        />
        <ChromeButton icon={PanelRight} sf="sidebar.right" title="Close the panel (⇧⌘D)" active onClick={onClose} />
      </div>
      <div className="panel-line" />
      <div className="panel-content">
        {shown === "diff" && <DiffPanel key={session.id} session={session} />}
        {shown === "files" && <FilesPanel key={session.cwd} session={session} />}
        {shown === "history" && <HistoryPanel key={session.id} session={session} />}
        {shown === "terminal" && <FolderTerminal key={session.cwd} folder={session.cwd} move="toBottom" />}
      </div>
    </aside>
  );
}
