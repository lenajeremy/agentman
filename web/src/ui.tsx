// Small pieces the Mac app draws everywhere (desktop/Agentman/Design/Theme.swift):
// ChromeButton, AgentMark, FolderMark, the app icon, and the icons standing
// in for SF Symbols (Lucide, drawn at SF's weights).

import { useState, type ReactNode } from "react";
import { createLucideIcon, type LucideIcon } from "lucide-react";
import type { Session } from "@shared/protocol";
import { fileURL } from "./editor/fs";
import { initials } from "./labels";

/** SF Symbols' wrench.and.screwdriver, which Lucide lacks: its wrench,
 *  crossed by a screwdriver from the top left. A run of steps' mark. */
export const WrenchAndScrewdriver = createLucideIcon("wrench-and-screwdriver", [
  ["path", { d: "M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.106-3.105c.32-.322.863-.22.983.218a6 6 0 0 1-8.259 7.057l-7.91 7.91a1 1 0 0 1-2.999-3l7.91-7.91a6 6 0 0 1 7.057-8.259c.438.12.54.662.219.984z", key: "wrench" }],
  ["path", { d: "M2.6 6.1a1 1 0 0 1 0-1.4l2.1-2.1a1 1 0 0 1 1.4 0l3.2 3.2a1 1 0 0 1 0 1.4l-2.1 2.1a1 1 0 0 1-1.4 0z", key: "handle" }],
  ["path", { d: "m9.5 9.5 11 11", key: "shaft" }],
]);

/** SF Symbols' circle.lefthalf.filled, which Lucide lacks (its Contrast
 *  fills a smaller half inside): the appearance switch's mark. */
export const CircleLeftHalfFilled = createLucideIcon("circle-left-half-filled", [
  ["circle", { cx: "12", cy: "12", r: "10", key: "ring" }],
  ["path", { d: "M12 2a10 10 0 0 0 0 20z", fill: "currentColor", key: "half" }],
]);

type Weight = "regular" | "medium" | "semibold" | "bold";

/**
 * An icon as the Mac app draws it: on macOS, the SF Symbol itself, drawn by
 * the system (the shell's /sf/ route, symbols.go) and used as a mask so it
 * takes the text colour; elsewhere, where SF Symbols may not be used, the
 * Lucide icon nearest to it. size and weight are the symbol's, in points.
 */
export function Icon({ icon: I, sf, size, weight = "regular", className }: { icon?: LucideIcon; sf?: string; size: number; weight?: Weight; className?: string }) {
  if (sf && document.documentElement.dataset.platform === "darwin") {
    const box = size * 1.5;
    const url = `url(/sf/${sf}.png?size=${size}&weight=${weight})`;
    return (
      <span
        className={`sf ${className ?? ""}`}
        aria-hidden
        style={{ width: box, height: box, margin: -(box - size) / 2, WebkitMaskImage: url, maskImage: url }}
      />
    );
  }
  if (!I) return null;
  const stroke = { regular: 1.7, medium: 2, semibold: 2.3, bold: 2.6 }[weight];
  return <I size={size + 1} strokeWidth={stroke} className={className} aria-hidden />;
}

/** 28×28, symbol 13 regular, muted (text when active), fill@0.55 on hover or active. */
export function ChromeButton({ icon, sf, glyph, active, title, onClick }: { icon?: LucideIcon; sf?: string; glyph?: ReactNode; active?: boolean; title: string; onClick?: () => void }) {
  const symbol = sf && document.documentElement.dataset.platform === "darwin";
  return (
    <button className={`chrome ${active ? "active" : ""}`} title={title} aria-label={title} onClick={onClick}>
      {symbol || !glyph ? <Icon icon={icon} sf={sf} size={13} /> : glyph}
    </button>
  );
}

// Marks drawn in the text colour, as the Mac's template images are; the
// others keep their own colours.
const TEMPLATE = new Set(["codex", "cursor", "cursor-cli", "opencode", "grok"]);
const KNOWN = new Set(["claude", "kiro", "gemini", "antigravity", "muse", ...TEMPLATE]);

/** muted: grey with the rest of an archived row, the logo's shape in the
 *  faint grey (AgentMark's template rendering). */
export function AgentMark({ kind, size, muted = false }: { kind: string; size: number; muted?: boolean }) {
  if (!KNOWN.has(kind)) {
    return <span className={`mark mark-none${muted ? " muted" : ""}`} style={{ width: size, height: size, fontSize: size * 0.75 }}>›_</span>;
  }
  const src = `/assets/agents/${kind}.${kind === "antigravity" ? "png" : "svg"}`;
  if (TEMPLATE.has(kind) || muted) {
    return <span className={`mark mark-template${muted ? " muted" : ""}`} style={{ width: size, height: size, WebkitMaskImage: `url(${src})`, maskImage: `url(${src})` }} />;
  }
  return <img className="mark" src={src} width={size} height={size} alt="" />;
}

/** Logos that couldn't be read, remembered as none (FolderLogos). */
const unreadable = new Set<string>();

/** Which folder an agent works in (Sidebar.swift FolderMark): the folder's
 *  own logo when am serve found one, otherwise its first two letters on the
 *  brand wash. */
export function FolderMark({ session, size = 16 }: { session: Pick<Session, "cwd" | "folderIcon">; size?: number }) {
  const [, redraw] = useState(0);
  const icon = session.folderIcon;
  const path = icon && !icon.includes("..") ? session.cwd.replace(/\/+$/, "") + "/" + icon : "";
  const shape = { width: size, height: size, borderRadius: size / 4 };
  if (path && !unreadable.has(path)) {
    return (
      <img
        className="folder-logo"
        src={fileURL(path)}
        style={shape}
        alt=""
        draggable={false}
        onError={() => {
          unreadable.add(path);
          redraw((n) => n + 1);
        }}
      />
    );
  }
  return (
    <span className="badge" style={size === 16 ? undefined : shape}>
      {initials(session.cwd)}
    </span>
  );
}

/** The macOS app icon, which draws its art inset in its frame (about 17 of 20). */
export function AppIcon({ size }: { size: number }) {
  const art = Math.round(size * 0.85);
  return (
    <span className="app-icon-frame" style={{ width: size, height: size }}>
      <img className="app-icon" src="/assets/agentman.png" width={art} height={art} alt="" />
    </span>
  );
}

/** SF Symbols' circle.inset.filled: a ring round a large filled disc, where
 *  Lucide's CircleDot has a small dot. A modified file's mark in a diff. */
export const CircleInsetFilled = createLucideIcon("circle-inset-filled", [
  ["circle", { cx: "12", cy: "12", r: "10", key: "ring" }],
  ["circle", { cx: "12", cy: "12", r: "6.5", fill: "currentColor", stroke: "none", key: "disc" }],
]);

/** SF Symbols' exclamationmark.circle.fill: a filled circle with the mark
 *  cut out of it, where Lucide's CircleAlert is an outline. */
export const CircleAlertFilled = createLucideIcon("circle-alert-filled", [
  ["path", {
    d: "M12 2a10 10 0 1 0 0 20a10 10 0 1 0 0-20zM10.9 7.1a1.1 1.1 0 0 1 2.2 0v5.6a1.1 1.1 0 0 1-2.2 0zM12 15.2a1.35 1.35 0 1 0 0 2.7a1.35 1.35 0 1 0 0-2.7z",
    fill: "currentColor", stroke: "none", fillRule: "evenodd", key: "mark",
  }],
]);

/** SF Symbols' plus.forwardslash.minus as a Lucide icon, for a button that
 *  takes one: the Board's Review. */
export const PlusSlashMinusIcon = createLucideIcon("plus-forwardslash-minus", [
  ["path", { d: "M7 3.5v7M3.5 7h7M16.5 4.5 7.5 19.5M13.5 17h7", key: "marks" }],
]);

/** SF Symbols' plus.forwardslash.minus, which Lucide has no match for. */
export function PlusSlashMinus({ size = 14 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.7} strokeLinecap="round" aria-hidden>
      <path d="M7 3.5v7M3.5 7h7M16.5 4.5 7.5 19.5M13.5 17h7" />
    </svg>
  );
}

export function Spacer({ min = 0 }: { min?: number }) {
  return <span style={{ flex: 1, minWidth: min }} />;
}

export function Row({ gap, children, className, style }: { gap: number; children: ReactNode; className?: string; style?: React.CSSProperties }) {
  return (
    <div className={`hstack ${className ?? ""}`} style={{ gap, ...style }}>
      {children}
    </div>
  );
}

/** A row of choices, one of them chosen (UsageView.swift's Segments): the
 *  Usage page's ranges and the sidebar filter's. */
export function Segments<T extends string | number | null>({ options, value, title, onChange }: { options: T[]; value: T; title: (v: T) => string; onChange: (v: T) => void }) {
  return (
    <span className="segments">
      {options.map((o) => (
        <button key={String(o)} className={o === value ? "chosen" : ""} aria-pressed={o === value} onClick={() => onChange(o)}>{title(o)}</button>
      ))}
    </span>
  );
}
