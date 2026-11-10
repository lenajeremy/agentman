// A floating search box with its results under it (Design/PaletteView.swift),
// in the manner of the command palettes in VS Code, Linear and Raycast:
// type, move with the arrow keys, Return to choose, Escape or a click
// outside to close.

import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import { Search } from "lucide-react";
import { Truncated } from "../shell/Truncated";
import { Icon } from "../ui";
import "../styles/palette.css";

/** One row in a palette: a command to run, an agent to open. */
export interface PaletteItem {
  id: string;
  title: string;
  detail?: string;
  /** The SF Symbol, and the Lucide icon standing in for it off macOS. */
  icon: string;
  lucide?: LucideIcon;
  /** A colour for the icon; muted by default. */
  tint?: string;
  /** A shortcut shown at the row's end, such as "⌘N". */
  shortcut?: string;
  /** Drawn instead of the icon, as a file's own. */
  glyph?: ReactNode;
}

/** A shortcut as the Mac writes it ("⌘N"); "Ctrl+N" where there is no ⌘.
 *  In a browser, the keys it keeps for itself (⌘N opens a browser window,
 *  and a page never sees it) aren't shown: there the shortcut doesn't work. */
export function shortcutLabel(shortcut: string): string {
  const os = document.documentElement.dataset.platform;
  if (os === "browser" && /^⌘[NTWQ]$/.test(shortcut)) return "";
  const mac = os === "darwin" || (os === "browser" && navigator.platform.startsWith("Mac"));
  return mac ? shortcut : shortcut.replace("⌘", "Ctrl+");
}

/**
 * Where the keyboard was when a palette opened, given back when it closes,
 * unless what was chosen took it somewhere else. Closing one with Escape
 * used to leave the keyboard nowhere, so what was typed next was lost.
 * `giveBack` does it where focusing the element isn't enough (the editor's
 * code, in a frame of its own).
 */
export function useKeyboardBack(giveBack?: (before: Element | null) => void) {
  const [before] = useState(() => document.activeElement);
  const back = useRef(giveBack);
  back.current = giveBack;
  useEffect(
    () => () => {
      requestAnimationFrame(() => {
        const now = document.activeElement;
        if (now && now !== document.body) return;
        if (back.current) back.current(before);
        else if (before instanceof HTMLElement && before.isConnected) before.focus();
      });
    },
    [before],
  );
}

export function PaletteView({ placeholder, results, choose, dismiss, initialQuery = "", status, giveBack }: {
  placeholder: string;
  /** The rows for what has been typed, best first. */
  results: (query: string) => PaletteItem[];
  choose: (item: PaletteItem) => void;
  dismiss: () => void;
  initialQuery?: string;
  status?: string;
  giveBack?: (before: Element | null) => void;
}) {
  useKeyboardBack(giveBack);
  const [query, setQuery] = useState(initialQuery);
  const [selected, setSelected] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const items = results(query);

  useEffect(() => {
    input.current?.focus();
  }, []);
  useEffect(() => setSelected(0), [query]);
  useLayoutEffect(() => {
    const row = list.current?.children[selected] as HTMLElement | undefined;
    row?.scrollIntoView({ block: "nearest" });
  }, [selected]);

  return (
    <div className="palette-overlay" onMouseDown={dismiss}>
      <div className="palette" onMouseDown={(e) => e.stopPropagation()}>
        <div className="palette-search">
          <Icon icon={Search} sf="magnifyingglass" size={13} weight="medium" />
          <input
            ref={input}
            value={query}
            placeholder={placeholder}
            spellCheck={false}
            aria-label={placeholder}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown") {
                e.preventDefault();
                setSelected((s) => Math.min(s + 1, Math.max(items.length - 1, 0)));
              } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setSelected((s) => Math.max(s - 1, 0));
              } else if (e.key === "Escape") {
                e.preventDefault();
                dismiss();
              } else if (e.key === "Enter" && !e.nativeEvent.isComposing) {
                e.preventDefault();
                if (items[selected]) choose(items[selected]);
              }
            }}
          />
        </div>
        {items.length > 0 ? (
          <>
            <div className="palette-rule" />
            <div className="palette-results" style={{ height: Math.min(items.length * 34 + 12, 380) }}>
              <div className="palette-rows" ref={list}>
                {items.map((item, index) => (
                  <div
                    key={item.id}
                    className={`palette-row ${index === selected ? "selected" : ""}`}
                    onMouseMove={() => index !== selected && setSelected(index)}
                    onClick={() => choose(item)}
                  >
                    <span className={`palette-icon ${item.icon === "circle.fill" ? "filled" : ""}`} style={{ color: item.tint }}>
                      {item.glyph ?? <Icon icon={item.lucide} sf={item.icon} size={12} />}
                    </span>
                    <span className="palette-title">{item.title}</span>
                    {item.detail && <Truncated mode="head" className="palette-detail" text={item.detail} />}
                    <span className="palette-gap" />
                    {item.shortcut && shortcutLabel(item.shortcut) && <span className="palette-shortcut">{shortcutLabel(item.shortcut)}</span>}
                  </div>
                ))}
              </div>
            </div>
            {status && (
              <>
                <div className="palette-rule" />
                <div className="palette-status">{status}</div>
              </>
            )}
          </>
        ) : query ? (
          <>
            <div className="palette-rule" />
            <div className="palette-empty">{status ?? "Nothing matches"}</div>
          </>
        ) : null}
      </div>
    </div>
  );
}
