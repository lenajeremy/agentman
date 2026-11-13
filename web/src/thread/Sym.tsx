// An SF Symbol laid out as SwiftUI lays out Image(systemName:): as wide and
// tall as the symbol's own image at its size and weight, which differ from
// symbol to symbol (wrench.and.screwdriver is 15 wide at 10 points, pencil
// 11). ui.tsx's Icon takes a square of the point size; here the measured
// sizes are kept, so a row's words start where the Mac's do. Off macOS the
// Lucide icon is drawn in the same box.

import type { LucideIcon } from "lucide-react";

type Weight = "regular" | "medium" | "semibold" | "bold";

/** NSImage(systemSymbolName:) sizes, in points, on macOS 26. */
const SIZES: Record<string, [number, number, number?]> = {
  "wrench.and.screwdriver:10:medium": [15, 14, 3.5],
  "terminal:10:medium": [14, 12, 2],
  "doc.text:10:medium": [11, 13, 3],
  "pencil:10:medium": [11, 11, 1.5],
  "magnifyingglass:10:medium": [12, 12, 2.5],
  "globe:10:medium": [13, 13, 2.5],
  "person.2:10:medium": [16, 12, 2.5],
  "checklist:10:medium": [14, 12, 2.5],
  "wrench:10:medium": [14, 15, 3.5],
  "xmark.circle.fill:10:medium": [13, 13, 2.5],
  "photo:10:medium": [14, 12, 2],
  "text.alignleft:10:medium": [13, 11, 2],
  "chevron.right:9:semibold": [7, 10],
  "doc.on.doc:11:regular": [14, 16],
  "checkmark:11:regular": [13, 12],
  "list.bullet.clipboard:11:medium": [13, 16],
  "folder:11:medium": [16, 13],
  "exclamationmark.circle:12:medium": [15, 15],
  "xmark:10:semibold": [11, 11],
  "arrow.down:13:semibold": [13, 15],
  "xmark:13:semibold": [14, 13],
  "moon.zzz:13:regular": [15, 17],
  "ellipsis:13:regular": [14, 5],
  "paperclip:14:regular": [16, 18],
  "photo.on.rectangle:14:medium": [21, 17],
  "arrow.up:13:bold": [14, 15],
  "chevron.down:8:bold": [10, 6],
  "xmark:8:bold": [9, 8],
  "return:11:semibold": [15, 13],
  "sparkle:11:medium": [13, 13],
  "sparkle:10:semibold": [12, 12],
  "chevron.left:15:semibold": [12, 17],
  "chevron.right:15:semibold": [12, 17],
  "photo:12:regular": [17, 13],
  "globe:11:regular": [13, 13],
  "mic:13:semibold": [15, 17],
  "mic.fill:13:semibold": [15, 17],
  "stop.fill:13:semibold": [14, 13],
  "return:9.5:semibold": [13, 11],
};

/**
 * text: the font size of the words beside it, for a row aligned on their
 * first baseline (HStack(alignment: .firstTextBaseline)): the symbol's own
 * baseline, the third number, meets theirs. The row puts the words' line
 * at the top, its line height AppKit's (thread.css --lh12).
 */
export function Sym({ sf, size, weight = "regular", icon: I, className, text }: {
  sf: string;
  size: number;
  weight?: Weight;
  icon?: LucideIcon;
  className?: string;
  text?: number;
}) {
  const [width, height, baseline = 0] = SIZES[`${sf}:${size}:${weight}`] ?? [size, size];
  const mac = document.documentElement.dataset.platform === "darwin";
  const box = size * 1.5;
  // AppKit puts a line's baseline round(ascent) down from its top.
  const style = { width, height, ...(text ? { marginTop: Math.round(0.9668 * text) - (height - baseline) } : {}) };
  if (mac) {
    const url = `url(/sf/${sf}.png?size=${size}&weight=${weight})`;
    return (
      <span className={`sym ${className ?? ""}`} style={style} aria-hidden>
        <span className="sym-mask" style={{ width: box, height: box, WebkitMaskImage: url, maskImage: url }} />
      </span>
    );
  }
  const stroke = { regular: 1.7, medium: 2, semibold: 2.3, bold: 2.6 }[weight];
  return (
    <span className={`sym ${className ?? ""}`} style={style} aria-hidden>
      {I ? <I size={size + 1} strokeWidth={stroke} /> : null}
    </span>
  );
}
