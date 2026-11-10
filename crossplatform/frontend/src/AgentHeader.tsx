// An agent's header (AgentThreadView.swift's title bar): "folder / name",
// then the servers menu, terminal, changes panel, workspace card and the
// more menu.

import { Ellipsis, PanelRight, SquareTerminal } from "lucide-react";
import type { Session } from "@shared/protocol";
import { folderLabel } from "./labels";
import { ChromeButton, Icon, PlusSlashMinus } from "./ui";

export function AgentHeader({ session, terminal, panel, card, onTerminal, onPanel, onCard }: {
  session: Session;
  terminal: boolean;
  panel: boolean;
  card: boolean;
  onTerminal: () => void;
  onPanel: () => void;
  onCard: () => void;
}) {
  return (
    <header className="detail-header drag">
      <div className="crumbs">
        <span className="crumb-folder">{folderLabel(session.cwd)}</span>
        <span className="crumb-slash">/</span>
        <span className="crumb-name no-drag">{session.name || session.kind}</span>
      </div>
      <div className="header-buttons no-drag">
        <ChromeButton icon={SquareTerminal} sf="terminal" title="Terminal" active={terminal} onClick={onTerminal} />
        <ChromeButton sf="plus.forwardslash.minus" glyph={<PlusSlashMinus />} title="Changes" active={panel} onClick={onPanel} />
        <ChromeButton icon={PanelRight} sf="sidebar.right" title="Workspace" active={card} onClick={onCard} />
        <button className="ellipsis" aria-label="More"><Icon icon={Ellipsis} sf="ellipsis" size={13} /></button>
      </div>
    </header>
  );
}
