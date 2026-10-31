// The card beside a conversation (Views/WorkspacePanel.swift): where the
// agent runs, its folder in the editor or a terminal, and its git.

import { ChevronDown, CircleCheck, Folder, GitFork, History, Monitor, SquareArrowOutUpRight, SquareTerminal, Diff } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import type { Session } from "@shared/protocol";
import { useStore } from "./store";
import { Icon } from "./ui";

function CardRow({ icon, sf, title, onClick, trailing }: { icon: LucideIcon; sf: string; title: string; onClick?: () => void; trailing?: ReactNode }) {
  return (
    <div className={`card-row ${onClick ? "clickable" : ""}`} onClick={onClick} role={onClick ? "button" : undefined}>
      <span className="card-icon"><Icon icon={icon} sf={sf} size={12} /></span>
      <span className="card-title">{title}</span>
      {trailing}
    </div>
  );
}

export function WorkspaceCard({ session, platform, onEditor, onTerminal }: { session: Session; platform: string; onEditor: () => void; onTerminal: () => void }) {
  const { branches, changes } = useStore();
  const branch = branches[session.cwd];
  const changed = changes[session.id];
  const place = platform === "darwin" ? "This Mac" : platform === "windows" ? "This PC (WSL)" : "This computer";
  return (
    <aside className="workspace-card">
      <section className="card-section">
        <CardRow icon={Monitor} sf="desktopcomputer" title={place} trailing={<span className="card-trailing-button"><Icon icon={Folder} sf="folder" size={12} /></span>} />
        <CardRow icon={SquareArrowOutUpRight} sf="arrow.up.right.square" title="Open in editor" onClick={onEditor} />
        <CardRow icon={SquareTerminal} sf="terminal" title="Open terminal" onClick={onTerminal} />
      </section>
      <section className="card-section">
        {branch && <CardRow icon={GitFork} sf="arrow.triangle.branch" title={branch} />}
        <CardRow icon={CircleCheck} sf="checkmark.circle" title="Commit" onClick={() => {}} trailing={<span className="card-menu"><Icon icon={ChevronDown} sf="chevron.down" size={9} weight="semibold" /></span>} />
        <CardRow icon={History} sf="clock.arrow.circlepath" title="History" onClick={() => {}} />
        <CardRow
          icon={Diff} sf="plusminus"
          title="Changes"
          onClick={() => {}}
          trailing={changed && (changed.added || changed.removed) ? (
            <span className="card-counts"><span className="added">+{changed.added}</span><span className="removed">−{changed.removed}</span></span>
          ) : undefined}
        />
        <CardRow icon={Folder} sf="folder" title="Files" onClick={() => {}} />
      </section>
    </aside>
  );
}
