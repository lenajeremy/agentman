// An icon laid out as SwiftUI lays out an SF Symbol: as wide as the symbol's
// own outline at that size and weight (its alignment rect), which is often
// wider than its point size, so what sits beside it lands where it does on
// the Mac. The widths are macOS's, read from NSImage.

import type { LucideIcon } from "lucide-react";
import { Icon } from "../ui";

type Weight = "regular" | "medium" | "semibold" | "bold";

/** Alignment-rect widths, by "name@size@weight". */
const WIDTHS: Record<string, number> = {
  "magnifyingglass@12@regular": 14.5,
  "magnifyingglass@13@medium": 16,
  "square.and.pencil@12@regular": 15,
  "line.3.horizontal.decrease@12@regular": 15,
  "folder@10@medium": 13.5,
  "folder@11@medium": 15.5,
  "arrow.triangle.branch@11@medium": 13.5,
  "chevron.down@8@bold": 9.5,
  "chevron.right@9@semibold": 7,
  "xmark@8@bold": 9,
  "xmark@10@bold": 10.5,
  "globe@9@regular": 11,
  "sparkles@12@regular": 13,
  "circle.fill@12@regular": 14,
  "arrow.up@13@bold": 13.5,
};

export function Sym({ icon, sf, size, weight = "regular", className }: { icon?: LucideIcon; sf: string; size: number; weight?: Weight; className?: string }) {
  const width = WIDTHS[`${sf}@${size}@${weight}`] ?? size;
  return (
    <span className={`sym ${className ?? ""}`} style={{ width, display: "inline-flex", justifyContent: "center", alignItems: "center", flex: "none" }}>
      <Icon icon={icon} sf={sf} size={size} weight={weight} />
    </span>
  );
}
