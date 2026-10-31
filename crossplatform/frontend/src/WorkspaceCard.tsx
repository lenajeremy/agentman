// The card beside a conversation (Views/WorkspacePanel.swift): where the
// agent runs, its folder in the editor or a terminal, and its git.

import { ChevronDown, CircleCheck, Folder, GitFork, History, Monitor, SquareArrowOutUpRight, SquareTerminal, Diff } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import type { Session } from "@shared/protocol";
import { useStore } from "./store";
import { Icon } from "./ui";

function CardRow({ icon, title, onClick, trailing }: { icon: LucideIcon; title: string; onClick?: () => void; trailing?: ReactNode }) {
  return (
    <div className={`card-row ${onClick ? "clickable" : ""}`} onClick={onClick} role={onClick ? "button" : undefined}>
      <span className="card-icon"><Icon icon={icon} size={14} /></span>
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
        <CardRow icon={Monitor} title={place} trailing={<span className="card-trailing-button"><Icon icon={Folder} size={13} /></span>} />
        <CardRow icon={SquareArrowOutUpRight} title="Open in editor" onClick={onEditor} />
        <CardRow icon={SquareTerminal} title="Open terminal" onClick={onTerminal} />
      </section>
      <section className="card-section">
        {branch && <CardRow icon={GitFork} title={branch} />}
        <CardRow icon={CircleCheck} title="Commit" onClick={() => {}} trailing={<span className="card-menu"><Icon icon={ChevronDown} size={10} weight="semibold" /></span>} />
        <CardRow icon={History} title="History" onClick={() => {}} />
        <CardRow
          icon={Diff}
          title="Changes"
          onClick={() => {}}
          trailing={changed && (changed.added || changed.removed) ? (
            <span className="card-counts"><span className="added">+{changed.added}</span><span className="removed">−{changed.removed}</span></span>
          ) : undefined}
        />
        <CardRow icon={Folder} title="Files" onClick={() => {}} />
      </section>
    </aside>
  );
}
