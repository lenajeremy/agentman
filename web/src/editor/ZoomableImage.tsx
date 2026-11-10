// A picture fitted to where it shows, never drawn larger than its own size,
// that zooms (Design/ZoomableImage.swift): pinch or ⌃-scroll, ⌘+ and ⌘−,
// ⌘0 to fit it again, a double-click for its actual size and back. Zoomed
// in, it scrolls.

import { useEffect, useLayoutEffect, useRef, useState } from "react";

const DEEPEST = 8;
const STEP = 1.25;

const fitScale = (w: number, h: number, sw: number, sh: number) =>
  w > 0 && h > 0 && sw > 0 && sh > 0 ? Math.min(1, sw / w, sh / h) : 1;
const actualSize = (fit: number) => (fit < 1 ? 1 / fit : 2);
const clamp = (zoom: number, fit: number) => Math.min(Math.max(zoom, 1), Math.max(DEEPEST / Math.max(fit, 0.0001), 1));

export function ZoomableImage({ src, alt = "", padding = 24 }: { src: string; alt?: string; padding?: number }) {
  const box = useRef<HTMLDivElement>(null);
  const [natural, setNatural] = useState<{ w: number; h: number } | null>(null);
  const [space, setSpace] = useState({ w: 0, h: 0 });
  const [zoom, setZoom] = useState(1);
  const anchor = useRef<{ x: number; y: number } | null>(null);

  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    const measure = () => setSpace({ w: el.clientWidth - padding * 2, h: el.clientHeight - padding * 2 });
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [padding]);

  useEffect(() => setZoom(1), [src]);

  const fit = natural ? fitScale(natural.w, natural.h, space.w, space.h) : 1;
  const scale = fit * zoom;

  // Zooming about a point keeps that point under the pointer.
  const zoomTo = (wanted: number, at?: { x: number; y: number }) => {
    const el = box.current;
    const next = clamp(wanted, fit);
    if (el && natural) {
      const point = at ?? { x: el.clientWidth / 2, y: el.clientHeight / 2 };
      anchor.current = {
        x: ((el.scrollLeft + point.x - padding) / scale) * fit * next - point.x + padding,
        y: ((el.scrollTop + point.y - padding) / scale) * fit * next - point.y + padding,
      };
    }
    setZoom(next);
  };

  useLayoutEffect(() => {
    const el = box.current;
    if (!el || !anchor.current) return;
    el.scrollLeft = anchor.current.x;
    el.scrollTop = anchor.current.y;
    anchor.current = null;
  }, [zoom]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.altKey) return;
      if (e.key === "+" || e.key === "=") zoomTo(zoom * STEP);
      else if (e.key === "-") zoomTo(Math.max(1, zoom / STEP));
      else if (e.key === "0") zoomTo(1);
      else return;
      e.preventDefault();
      e.stopImmediatePropagation();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  });

  const width = natural ? natural.w * scale : undefined;
  const height = natural ? natural.h * scale : undefined;
  return (
    <div
      ref={box}
      className="zoomable"
      style={{ padding }}
      onWheel={(e) => {
        if (!e.ctrlKey) return;
        e.preventDefault();
        const rect = e.currentTarget.getBoundingClientRect();
        zoomTo(zoom * Math.exp(-e.deltaY / 100), { x: e.clientX - rect.left, y: e.clientY - rect.top });
      }}
      onDoubleClick={(e) => {
        const rect = e.currentTarget.getBoundingClientRect();
        zoomTo(zoom === 1 ? actualSize(fit) : 1, { x: e.clientX - rect.left, y: e.clientY - rect.top });
      }}
    >
      <div className="zoomable-stage" style={{ minWidth: width, minHeight: height }}>
        <img
          src={src}
          alt={alt}
          draggable={false}
          style={{ width, height, visibility: natural ? "visible" : "hidden" }}
          onLoad={(e) => setNatural({ w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })}
        />
      </div>
    </div>
  );
}
