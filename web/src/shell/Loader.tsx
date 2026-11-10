// What an agent shows while it works (Design/Loader.swift): a ring chasing
// round, its tail fading and its length breathing, its colours flowing
// along it. Each colour rests on the whole ring a moment, then the next
// comes in at the head and spreads back to the tail. Still when the system
// is asked to reduce motion. Every loader on screen draws from one clock.

import { useEffect, useRef } from "react";

/** Seconds each colour has before the next has taken over. */
const HOLD = 1.2;
/** How much of a turn from one colour to the next the ring spans at once. */
const SPREAD = 0.35;
/** The part of its turn a colour rests before the next comes in. */
const REST = 0.6;

const canvases = new Map<HTMLCanvasElement, number>();
let frame = 0;
const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)");

function rgb(hex: string): [number, number, number] {
  const h = hex.trim().replace("#", "");
  const n = parseInt(h.length === 3 ? h.split("").map((c) => c + c).join("") : h, 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

function colorAt(phase: number, colors: [number, number, number][]): [number, number, number] {
  const step = Math.floor(phase);
  const index = ((step % colors.length) + colors.length) % colors.length;
  const x = Math.min(1, Math.max(0, (phase - step - REST) / (1 - REST)));
  const t = x * x * (3 - 2 * x);
  const a = colors[index];
  const b = colors[(index + 1) % colors.length];
  return [a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t, a[2] + (b[2] - a[2]) * t];
}

function draw(canvas: HTMLCanvasElement, size: number, time: number, colors: [number, number, number][]) {
  const scale = window.devicePixelRatio || 1;
  const px = Math.round(size * scale);
  if (canvas.width !== px) {
    canvas.width = px;
    canvas.height = px;
  }
  const ctx = canvas.getContext("2d");
  if (!ctx) return;
  ctx.setTransform(scale, 0, 0, scale, 0, 0);
  ctx.clearRect(0, 0, size, size);
  const width = Math.max(1.6, size * 0.18);
  const radius = (size - width) / 2;
  // The tail starts a little in, so its round cap stays in the fade.
  const start = 0.04;
  const end = start + 0.5 + 0.22 * Math.sin((time * Math.PI * 2) / 1.44);
  const turn = ((time * 400) % 360) * (Math.PI / 180);
  const samples = 24;
  ctx.lineWidth = width;
  ctx.lineCap = "round";
  for (let i = 0; i < samples; i++) {
    const along = (i + 0.5) / samples;
    const from = start + (end - start) * (i / samples);
    const to = start + (end - start) * ((i + 1) / samples);
    const [r, g, b] = colorAt(time / HOLD + along * SPREAD, colors);
    ctx.strokeStyle = `rgba(${r | 0}, ${g | 0}, ${b | 0}, ${Math.min(1, along / 0.25)})`;
    // Square joints between the pieces; only the head is round.
    ctx.lineCap = i === samples - 1 ? "round" : "butt";
    ctx.beginPath();
    ctx.arc(size / 2, size / 2, radius, turn + from * Math.PI * 2, turn + Math.min(to + 0.004, end) * Math.PI * 2);
    ctx.stroke();
  }
}

function tick() {
  frame = 0;
  const style = getComputedStyle(document.documentElement);
  const colors = [rgb(style.getPropertyValue("--working") || "#3268f5"), rgb(style.getPropertyValue("--loader-2") || "#f0407f")];
  const time = reduceMotion.matches ? 0 : performance.now() / 1000;
  for (const [canvas, size] of canvases) draw(canvas, size, time, colors);
  if (canvases.size && !reduceMotion.matches) frame = requestAnimationFrame(tick);
}

export function Loader({ size = 11 }: { size?: number }) {
  const ref = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    const canvas = ref.current;
    if (!canvas) return;
    canvases.set(canvas, size);
    if (!frame) frame = requestAnimationFrame(tick);
    return () => {
      canvases.delete(canvas);
    };
  }, [size]);
  return <canvas ref={ref} className="ring-loader" style={{ width: size, height: size }} role="img" aria-label="Working" />;
}
