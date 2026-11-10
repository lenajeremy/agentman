// A row of the workspace card (WorkspacePanel.swift's CardRow): an icon in a
// column of its own, its words, what goes at its end. Each row 30 high, the
// icon 16 wide 8 from the section's edge, the words 10 after it, so the card
// reads as one list. A row that does something lights up under the pointer.

import type { ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import { Icon } from "../ui";

export function CardRow({ title, icon, sf, glyph, onClick, trailing, help }: {
  title: string;
  icon?: LucideIcon;
  sf?: string;
  /** In place of a symbol: an agent's mark. */
  glyph?: ReactNode;
  onClick?: () => void;
  trailing?: ReactNode;
  help?: string;
}) {
  return (
    <div
      className={`card-row ${onClick ? "clickable" : ""}`}
      onClick={onClick}
      role={onClick ? "button" : undefined}
      title={help}
    >
      <span className="card-icon">{glyph ?? <Icon icon={icon} sf={sf} size={12} />}</span>
      <span className="card-title">{title}</span>
      <span className="card-spacer" />
      {trailing}
    </div>
  );
}
