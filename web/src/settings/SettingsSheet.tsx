// Settings as a sheet over the page, in a browser (settings/overPage.ts): the
// Settings window's sidebar and pages, as large as the Mac's window, on the
// page dimmed. The ✕, Escape or a click outside closes it, and the keyboard
// goes back where it was.

import { useEffect, useRef } from "react";
import { X } from "lucide-react";
import { Icon } from "../ui";
import { SettingsWindow } from "./SettingsWindow";
import { closeSettingsSheet, useSettingsSheet, type SheetAsk } from "./overPage";

export function SettingsSheet() {
  const asked = useSettingsSheet();
  return asked ? <Sheet asked={asked} /> : null;
}

function Sheet({ asked }: { asked: SheetAsk }) {
  const panel = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const before = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    // The keyboard in the sheet (not on a page's button, which would ring it).
    panel.current?.focus({ preventScroll: true });
    const onKey = (e: KeyboardEvent) => {
      // Not when a menu, or a sheet of Settings' own, took it first.
      if (e.key !== "Escape" || e.defaultPrevented || document.querySelector(".page-menu")) return;
      e.preventDefault();
      closeSettingsSheet();
    };
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      if (before?.isConnected) before.focus({ preventScroll: true });
    };
  }, []);

  return (
    <div className="settings-sheet-backdrop" onMouseDown={(e) => e.target === e.currentTarget && closeSettingsSheet()}>
      <div className="settings-sheet" ref={panel} tabIndex={-1} role="dialog" aria-modal="true" aria-label="Settings">
        <SettingsWindow asked={asked} />
        <button className="settings-sheet-close" title="Close (Esc)" aria-label="Close Settings" onClick={closeSettingsSheet}>
          <Icon icon={X} sf="xmark" size={11} weight="bold" />
        </button>
      </div>
    </div>
  );
}
