// A floating search box with its results under it (Design/PaletteView.swift),
// in the manner of the command palettes in VS Code, Linear and Raycast:
// type, move with the arrow keys, Return to choose, Escape or a click
// outside to close.

import { Fragment, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
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
  /** A line under the title: what a message said, its words marked. */
  snippet?: ReactNode;
  /** The group it's listed in, its heading over the group's first row. */
  section?: string;
}

/** How to use it, at its foot while it has nothing else to say. */
const HINTS = "↑↓ to move · ↵ to open · esc to close";

/** A group's heading, over the first of its rows. */
function heading(items: PaletteItem[], index: number): string | undefined {
  const section = items[index].section;
  return section && (index === 0 || items[index - 1].section !== section) ? section : undefined;
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
  const [before] = useState(keyboardNow);
  const back = useRef(giveBack);
  back.current = giveBack;
  // At once as it closes: a frame later, keys typed meanwhile were lost (in
  // Safari's engine, enough of them to notice). What the choice gives the
  // keyboard to afterwards, a terminal say, still takes it.
  useEffect(
    () => () => {
      const now = document.activeElement;
      if (now && now !== document.body) return;
      if (back.current) back.current(before.element);
      else giveKeyboardBack(before);
    },
    [before],
  );
}

/** Where the keyboard is: the element, and where the cursor is in it. */
export interface KeyboardPlace {
  element: Element | null;
  range: Range | null;
}

export function keyboardNow(): KeyboardPlace {
  const element = document.activeElement;
  const selection = window.getSelection();
  const range = selection && selection.rangeCount && element?.contains(selection.getRangeAt(0).startContainer) ? selection.getRangeAt(0).cloneRange() : null;
  return { element, range };
}

/** The keyboard back where it was, the cursor too: focused again, a message
 *  box would put it at the start of what's typed there. */
export function giveKeyboardBack(place: KeyboardPlace) {
  const { element, range } = place;
  if (!(element instanceof HTMLElement) || !element.isConnected) return;
  element.focus();
  if (!element.isContentEditable) return;
  const selection = window.getSelection();
  if (!selection) return;
  selection.removeAllRanges();
  if (range && element.contains(range.startContainer)) {
    selection.addRange(range);
  } else {
    const end = document.createRange();
    end.selectNodeContents(element);
    end.collapse(false);
    selection.addRange(end);
  }
}

export function PaletteView({ placeholder, results, choose, dismiss, initialQuery = "", status, giveBack, typed }: {
  placeholder: string;
  /** The rows for what has been typed, best first. */
  results: (query: string) => PaletteItem[];
  choose: (item: PaletteItem) => void;
  dismiss: () => void;
  initialQuery?: string;
  status?: string;
  giveBack?: (before: Element | null) => void;
  /** Told what is typed, as it changes (for a search of what was said). */
  typed?: (query: string) => void;
}) {
  useKeyboardBack(giveBack);
  const [query, setQuery] = useState(initialQuery);
  useEffect(() => typed?.(query), [query, typed]);
  const [selected, setSelected] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const items = results(query);

  useEffect(() => {
    input.current?.focus();
  }, []);
  useEffect(() => setSelected(0), [query]);
  useLayoutEffect(() => {
    const row = list.current?.querySelector(`[data-index="${selected}"]`) as HTMLElement | null;
    row?.scrollIntoView({ block: "nearest" });
  }, [selected]);
  // Rows of 34, 52 with a snippet, and 28 more over a group's first.
  const height = items.reduce((total, item, index) => total + (item.snippet ? 52 : 34) + (heading(items, index) ? 28 : 0), 0);

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
            <div className="palette-results" style={{ height: Math.min(height + 12, 380) }}>
              <div className="palette-rows" ref={list}>
                {items.map((item, index) => {
                  const group = heading(items, index);
                  return (
                    <Fragment key={item.id}>
                      {group && <div className={`palette-heading${index === 0 ? " first" : ""}`}>{group}</div>}
                      <div
                        data-index={index}
                        className={`palette-row ${item.snippet ? "with-snippet " : ""}${index === selected ? "selected" : ""}`}
                        onMouseMove={() => index !== selected && setSelected(index)}
                        onClick={() => choose(item)}
                      >
                        <div className="palette-item-line">
                          <span className={`palette-icon ${item.icon === "circle.fill" ? "filled" : ""}`} style={{ color: item.tint }}>
                            {item.glyph ?? <Icon icon={item.lucide} sf={item.icon} size={12} />}
                          </span>
                          <span className="palette-title">{item.title}</span>
                          {item.detail && <Truncated mode="head" className="palette-detail" text={item.detail} />}
                          <span className="palette-gap" />
                          {item.shortcut && shortcutLabel(item.shortcut) && <span className="palette-shortcut">{shortcutLabel(item.shortcut)}</span>}
                        </div>
                        {item.snippet && <div className="palette-snippet">{item.snippet}</div>}
                      </div>
                    </Fragment>
                  );
                })}
              </div>
            </div>
            {/* What it's doing, or how to use it. */}
            <div className="palette-rule" />
            <div className="palette-status">{status ?? HINTS}</div>
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
