// Small pieces the Mac app draws everywhere (desktop/Agentman/Design/Theme.swift):
// ChromeButton, AgentMark, the app icon, and the icons standing in for SF
// Symbols (Lucide, drawn at SF's weights).

import type { ReactNode } from "react";
import type { LucideIcon } from "lucide-react";

/** SF Symbols draw "regular" at about 1.6 of Lucide's 24-unit stroke. */
export function Icon({ icon: I, size, weight = "regular", className }: { icon: LucideIcon; size: number; weight?: "regular" | "medium" | "semibold" | "bold"; className?: string }) {
  const stroke = { regular: 1.7, medium: 2, semibold: 2.3, bold: 2.6 }[weight];
  return <I size={size} strokeWidth={stroke} absoluteStrokeWidth={false} className={className} aria-hidden />;
}

/** 28×28, symbol 13 regular, muted (text when active), fill@0.55 on hover or active. */
export function ChromeButton({ icon, glyph, active, title, onClick }: { icon?: LucideIcon; glyph?: ReactNode; active?: boolean; title: string; onClick?: () => void }) {
  return (
    <button className={`chrome ${active ? "active" : ""}`} title={title} aria-label={title} onClick={onClick}>
      {glyph ?? (icon && <Icon icon={icon} size={14} />)}
    </button>
  );
}

// Marks drawn in the text colour, as the Mac's template images are; the
// others keep their own colours.
const TEMPLATE = new Set(["codex", "cursor", "cursor-cli", "opencode", "grok"]);
const KNOWN = new Set(["claude", "kiro", "gemini", "antigravity", ...TEMPLATE]);

export function AgentMark({ kind, size }: { kind: string; size: number }) {
  if (!KNOWN.has(kind)) {
    return <span className="mark mark-none" style={{ width: size, height: size, fontSize: size * 0.75 }}>›_</span>;
  }
  const src = `/assets/agents/${kind}.${kind === "antigravity" ? "png" : "svg"}`;
  if (TEMPLATE.has(kind)) {
    return <span className="mark mark-template" style={{ width: size, height: size, WebkitMaskImage: `url(${src})`, maskImage: `url(${src})` }} />;
  }
  return <img className="mark" src={src} width={size} height={size} alt="" />;
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
