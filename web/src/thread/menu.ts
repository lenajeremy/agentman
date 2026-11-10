// The system's own menus and alerts (menus.go): SwiftUI's Menu and
// contextMenu are NSMenus, and confirmationDialog an alert sheet, so here
// they are the same, drawn by macOS (or GTK). In a plain browser, while the
// screens are developed, a page-drawn menu and confirm() stand in.

import { Call, Events } from "@wailsio/runtime";

export interface MenuEntry {
  label?: string;
  action?: () => void;
  checked?: boolean;
  disabled?: boolean;
  /** A section's heading, as SwiftUI's Section(title) in a menu. */
  header?: boolean;
  separator?: boolean;
}

const inShell = location.protocol === "wails:" || location.hostname === "wails.localhost";

let next = 0;
let open: { id: string; actions: Map<string, () => void> } | null = null;

if (inShell) {
  Events.On("agentman:menu", (event) => {
    const choice = event.data as { menu: string; key: string };
    if (!open || choice.menu !== open.id) return;
    const action = open.actions.get(choice.key);
    open = null;
    action?.();
  });
}

/** Opens a menu at x, y in the page. */
export function popupMenu(entries: MenuEntry[], x: number, y: number) {
  const id = `menu-${++next}`;
  const actions = new Map<string, () => void>();
  const items = entries.map((entry, i) => {
    const key = String(i);
    if (entry.action) actions.set(key, entry.action);
    return {
      key,
      label: entry.label ?? "",
      header: !!entry.header,
      separator: !!entry.separator,
      checked: !!entry.checked,
      disabled: !!entry.disabled,
    };
  });
  open = { id, actions };
  if (inShell) {
    void Call.ByName("main.Menus.Popup", id, items, Math.round(x), Math.round(y));
  } else {
    pageMenu(entries, x, y);
  }
}

/** A button's menu, under it, as a borderless SwiftUI Menu opens. */
export function menuUnder(button: HTMLElement, entries: MenuEntry[]) {
  const box = button.getBoundingClientRect();
  popupMenu(entries, box.left, box.bottom + 4);
}

/** A right-click's menu, at the pointer. */
export function contextMenu(event: { clientX: number; clientY: number; preventDefault(): void; stopPropagation(): void }, entries: MenuEntry[]) {
  event.preventDefault();
  event.stopPropagation();
  popupMenu(entries, event.clientX, event.clientY);
}

/** Asks in an alert sheet; answers with the button pressed. The last
 *  button is Cancel. */
export async function confirmSheet(title: string, message: string, buttons: string[]): Promise<string> {
  if (inShell) return (await Call.ByName("main.Menus.Confirm", { title, message, buttons })) as string;
  return window.confirm(`${title}\n\n${message}`) ? buttons[0] : buttons[buttons.length - 1];
}

/** In a browser: a menu drawn by the page, closed by a click anywhere. */
function pageMenu(entries: MenuEntry[], x: number, y: number) {
  document.querySelector(".page-menu")?.remove();
  const menu = document.createElement("div");
  menu.className = "page-menu";
  Object.assign(menu.style, {
    position: "fixed", left: `${x}px`, top: `${y}px`, zIndex: "1000", minWidth: "160px", padding: "5px",
    background: "var(--surface)", border: "1px solid var(--line)", borderRadius: "8px",
    boxShadow: "0 6px 20px rgba(0,0,0,0.15)", font: "13px var(--sans)",
  });
  for (const entry of entries) {
    const row = document.createElement("div");
    if (entry.separator) {
      Object.assign(row.style, { height: "1px", background: "var(--line)", margin: "5px 4px" });
    } else {
      row.textContent = (entry.checked ? "✓ " : "") + (entry.label ?? "");
      Object.assign(row.style, {
        padding: "3px 10px", borderRadius: "4px", color: entry.header || entry.disabled ? "var(--faint)" : "var(--text)",
        fontSize: entry.header ? "11px" : "13px", fontWeight: entry.header ? "600" : "400",
      });
      if (entry.action && !entry.disabled && !entry.header) {
        row.onmouseenter = () => (row.style.background = "var(--fill)");
        row.onmouseleave = () => (row.style.background = "");
        row.onclick = () => {
          menu.remove();
          entry.action?.();
        };
      }
    }
    menu.appendChild(row);
  }
  document.body.appendChild(menu);
  setTimeout(() => document.addEventListener("mousedown", function close(e) {
    if (!menu.contains(e.target as Node)) {
      menu.remove();
      document.removeEventListener("mousedown", close);
    }
  }), 0);
}
