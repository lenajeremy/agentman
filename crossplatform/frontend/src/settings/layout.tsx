// The pieces every Settings page is made of (Views/SettingsView.swift):
// a page with its title, sections on a card with a heading and a footer,
// and rows with what a setting is on the left and its control on the right.

import { useEffect, useRef, type CSSProperties, type ReactNode } from "react";

/** A settings page: its title, then its sections, 28 apart, scrolling. */
export function SettingsPage({ title, children }: { title: string; children: ReactNode }) {
  const scroll = useTestScroll();
  return (
    <div className="settings-scroll" ref={scroll}>
      <div className="settings-page">
        <h1 className="settings-display">{title}</h1>
        {children}
      </div>
    </div>
  );
}

/** For comparisons: scroll=<points> scrolls the page down once its content
 *  has come, as AGENTMAN_TEST_SCROLL does the Mac's. */
export function useTestScroll() {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const offset = Number(new URLSearchParams(location.search).get("scroll"));
    if (!offset) return;
    // As NSScrollView's, the offset counts from under the title bar.
    const titlebar = parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--titlebar")) || 0;
    const timer = window.setInterval(() => ref.current?.scrollTo(0, offset + titlebar), 250);
    const stop = window.setTimeout(() => window.clearInterval(timer), 6000);
    return () => {
      window.clearInterval(timer);
      window.clearTimeout(stop);
    };
  }, []);
  return ref;
}

/** Text wrapped as AppKit's standard line breaking does: never one word
 *  alone on the last line. The last two words are kept together. */
export function keepLast<T>(text: T): T {
  return (typeof text === "string" ? text.replace(/ (\S+)$/, "\u00a0$1") : text) as T;
}

/** A group of rows on one card, with a heading above and a note below. */
export function Section({ title, footer, children, className, style, cardClass }: { title?: string; footer?: ReactNode; children: ReactNode; className?: string; style?: CSSProperties; cardClass?: string }) {
  return (
    <section className={`settings-section ${className ?? ""}`} style={style}>
      {title && <div className="section-title">{title}</div>}
      <div className={`section-card ${cardClass ?? ""}`}>{children}</div>
      {footer && <div className="section-footer">{keepLast(footer)}</div>}
    </section>
  );
}

/** One setting: its title and detail, then its control. */
export function Row({ title, detail, divider = true, children, className, help }: { title: string; detail?: ReactNode; divider?: boolean; children?: ReactNode; className?: string; help?: string }) {
  return (
    <div className={`settings-row ${className ?? ""}`} title={help}>
      <div className="row-main">
        <div className="row-text">
          <div className="row-title">{title}</div>
          {detail && <div className="row-detail">{keepLast(detail)}</div>}
        </div>
        <span className="row-spacer" />
        {children}
      </div>
      {divider && <div className="row-divider" />}
    </div>
  );
}

/** A divider across a card, as SwiftUI's Divider(). */
export function Divider({ inset = 0 }: { inset?: number }) {
  return <div className="settings-divider" style={{ marginLeft: inset }} />;
}
