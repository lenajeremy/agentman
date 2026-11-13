// A QR code drawn as one SVG path, sharp at any size, as the Mac's QRCode
// view draws Core Image's (Views/DevicesSettings.swift): medium error
// correction, the smallest version that holds the text, no quiet zone (the
// card around it gives the margin).

import { useMemo } from "react";
import qrcode from "qrcode-generator";

/** The dark modules as a path of unit squares, and how many a side has. */
export function qrPath(text: string): { size: number; path: string } {
  const qr = qrcode(0, "M");
  qr.addData(text, "Byte");
  qr.make();
  const size = qr.getModuleCount();
  let path = "";
  for (let row = 0; row < size; row++) {
    // A run of dark modules along a row is one rectangle.
    for (let col = 0; col < size; col++) {
      if (!qr.isDark(row, col)) continue;
      let end = col;
      while (end + 1 < size && qr.isDark(row, end + 1)) end++;
      path += `M${col} ${row}h${end - col + 1}v1h-${end - col + 1}z`;
      col = end;
    }
  }
  return { size, path };
}

export function QRCode({ text, label }: { text: string; label: string }) {
  const { size, path } = useMemo(() => qrPath(text), [text]);
  return (
    <svg className="qr-code" viewBox={`0 0 ${size} ${size}`} role="img" aria-label={label} shapeRendering="crispEdges">
      <path d={path} fill="#000" />
    </svg>
  );
}
