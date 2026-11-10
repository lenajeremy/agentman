// Small pieces the Board's cards share.

import type { LucideIcon } from "lucide-react";
import { Icon } from "../ui";

/** Something to do with an agent, a click away, as a small chip; warns,
 *  before something that can't be taken back (BoardView.swift's BoardLink). */
export function BoardLink({ title, icon, sf, onClick, warns = false, help }: {
  title: string;
  icon: LucideIcon;
  sf: string;
  onClick: () => void;
  warns?: boolean;
  help?: string;
}) {
  return (
    <button className={`board-link${warns ? " warns" : ""}`} title={help} onClick={onClick}>
      <Icon icon={icon} sf={sf} size={10} />
      <span>{title}</span>
    </button>
  );
}
