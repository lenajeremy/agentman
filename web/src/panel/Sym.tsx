// An SF Symbol taking the room SwiftUI gives it in a row: its own width at
// its size and weight, measured on macOS (NSImage(systemSymbolName:) sizes),
// not its point size, so the words after it start where the Mac's do. On
// Linux the Lucide icon stands in, in the same room.

import type { LucideIcon } from "lucide-react";
import { Icon } from "../ui";

type Weight = "regular" | "medium" | "semibold" | "bold";

const WIDTHS: Record<string, number> = {
  "doc.text|11|regular": 13, "folder|11|regular": 16, "clock.arrow.circlepath|11|regular": 14, "terminal|11|regular": 16,
  "xmark|8|bold": 9, "xmark|9|semibold": 10, "plus|11|medium": 12,
  "chevron.down|8|semibold": 10, "chevron.right|8|semibold": 7, "chevron.right|9|semibold": 7, "chevron.down|9|semibold": 10,
  "point.3.connected.trianglepath.dotted|12|medium": 16,
  "plus.circle|11.5|regular": 14, "minus.circle|11.5|regular": 14, "arrow.right.circle|11.5|regular": 14, "circle.inset.filled|11.5|regular": 14,
  "terminal|10|regular": 14, "square.split.2x1|10|regular": 11,
  "arrow.up.and.down|9|medium": 8, "magnifyingglass|11|regular": 13, "xmark.circle.fill|11|regular": 13,
  "folder|12|regular": 17, "desktopcomputer|12|regular": 17, "checkmark.circle|12|regular": 14,
};

export function symbolWidth(sf: string, size: number, weight: Weight = "regular"): number {
  return WIDTHS[`${sf}|${size}|${weight}`] ?? size;
}

export function Sym({ sf, icon, size, weight = "regular", className }: { sf: string; icon?: LucideIcon; size: number; weight?: Weight; className?: string }) {
  return (
    <span className={`sym ${className ?? ""}`} style={{ width: symbolWidth(sf, size, weight) }}>
      <Icon icon={icon} sf={sf} size={size} weight={weight} />
    </span>
  );
}
