// A sheet over the Settings window, as SwiftUI's .sheet is on the Mac: a
// panel of its own below the title bar, centred, the window behind it out of
// reach until it closes. Escape cancels; Return presses the default button.

import { useEffect, useRef, type ReactNode } from "react";

export function Sheet({ width, children, onCancel, onDefault }: { width: number; children: ReactNode; onCancel: () => void; onDefault?: () => void }) {
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onCancel();
      } else if (e.key === "Enter" && onDefault && !(e.target instanceof HTMLTextAreaElement)) {
        e.preventDefault();
        onDefault();
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [onCancel, onDefault]);
  return (
    <div className="sheet-backdrop" onMouseDown={(e) => e.target === e.currentTarget && e.preventDefault()}>
      <div className="sheet" ref={box} style={{ width }} role="dialog" aria-modal>
        {children}
      </div>
    </div>
  );
}
