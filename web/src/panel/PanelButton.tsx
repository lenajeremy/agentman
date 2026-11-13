// A small icon button for a panel's toolbar (RightPanel.swift's PanelButton):
// 26×24, its symbol at 11.5, lit while what it toggles is on.

import type { LucideIcon } from "lucide-react";
import { Icon } from "../ui";

export function PanelButton({ sf, icon, help, on, disabled, onClick }: {
  sf: string; icon?: LucideIcon; help: string; on?: boolean; disabled?: boolean; onClick: () => void;
}) {
  return (
    <button className={`panel-button ${on ? "on" : ""}`} title={help} aria-label={help} disabled={disabled} onClick={onClick}>
      <Icon icon={icon} sf={sf} size={11.5} />
    </button>
  );
}
