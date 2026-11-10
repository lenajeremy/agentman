// Small pieces the Mac app draws everywhere (desktop/Agentman/Design/Theme.swift):
// ChromeButton, AgentMark, FolderMark, the app icon, and the icons standing
// in for SF Symbols (Lucide, drawn at SF's weights).

import { useState, type ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import type { Session } from "@shared/protocol";
import { fileURL } from "./editor/fs";
import { initials } from "./labels";

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
