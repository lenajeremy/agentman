// Text cut where SwiftUI's .truncationMode cuts it when it doesn't fit: in
// the middle (both ends show) or at the head (the end shows), which CSS
// can't do by itself. The span takes the room its flex parent gives it.

import { useLayoutEffect, useRef, type CSSProperties } from "react";

export function Truncated({ text, mode, className, title, style }: { text: string; mode: "middle" | "head"; className?: string; title?: string; style?: CSSProperties }) {
  const ref = useRef<HTMLSpanElement>(null);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const fit = () => {
      el.textContent = text;
      const room = el.clientWidth;
      if (el.scrollWidth <= room + 0.5) return;
      const chars = [...text];
      const cut = (keep: number) => {
        if (mode === "head") return "…" + chars.slice(chars.length - keep).join("");
        const head = Math.floor(keep / 2);
        return chars.slice(0, head).join("") + "…" + chars.slice(chars.length - (keep - head)).join("");
      };
      let lo = 0;
      let hi = chars.length - 1;
      let best = "…";
      while (lo <= hi) {
        const keep = (lo + hi) >> 1;
        el.textContent = cut(keep);
        if (el.scrollWidth <= room + 0.5) {
          best = el.textContent;
          lo = keep + 1;
        } else hi = keep - 1;
      }
      el.textContent = best;
    };
    fit();
    const observer = new ResizeObserver(fit);
    if (el.parentElement) observer.observe(el.parentElement);
    return () => observer.disconnect();
  }, [text, mode]);
  return <span ref={ref} className={className} title={title} style={{ whiteSpace: "nowrap", overflow: "hidden", minWidth: 0, ...style }} />;
}
