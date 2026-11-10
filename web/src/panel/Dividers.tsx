// The lines dragged to resize the workspace's panes (Views/Sidebar.swift's
// PaneResizeHandle, Terminal/TerminalPanel.swift's PanelDivider).

import { useRef } from "react";

/** The right panel's leading edge: a hairline with 8 to grab; dragging left
 *  widens the panel, a double-click puts back its default width. */
export function PaneResizeHandle({ width, onChange, reset }: { width: number; onChange: (width: number) => void; reset: () => void }) {
  const start = useRef<{ x: number; width: number } | null>(null);
  return (
    <div className="pane-edge" aria-hidden>
      <div
        className="pane-grab"
        onPointerDown={(e) => {
          if (e.button !== 0) return;
          e.currentTarget.setPointerCapture(e.pointerId);
          start.current = { x: e.clientX, width };
        }}
        onPointerMove={(e) => {
          if (!start.current) return;
          onChange(start.current.width - (e.clientX - start.current.x));
        }}
        onPointerUp={() => (start.current = null)}
        onPointerCancel={() => (start.current = null)}
        onDoubleClick={reset}
      />
    </div>
  );
}

/** The line over the terminal under the conversation: drag it up to make the
 *  terminal taller, never under 120. */
export function PanelDivider({ height, maximum, onChange }: { height: number; maximum: number; onChange: (height: number) => void }) {
  const start = useRef<{ y: number; height: number } | null>(null);
  return (
    <div
      className="bottom-divider"
      aria-hidden
      onPointerDown={(e) => {
        if (e.button !== 0) return;
        e.currentTarget.setPointerCapture(e.pointerId);
        start.current = { y: e.clientY, height };
      }}
      onPointerMove={(e) => {
        if (!start.current) return;
        const next = start.current.height - (e.clientY - start.current.y);
        onChange(Math.min(Math.max(next, 120), Math.max(maximum, 160)));
      }}
      onPointerUp={() => (start.current = null)}
      onPointerCancel={() => (start.current = null)}
    />
  );
}
