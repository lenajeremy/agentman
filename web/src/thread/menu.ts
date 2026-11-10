// The system's own menus and alerts (menus.go): SwiftUI's Menu and
// contextMenu are NSMenus, and confirmationDialog an alert sheet, so here
// they are the same, drawn by macOS (or GTK). In the browser the page draws
// them, in macOS's look (styles/menu.css), with the same keys.

import { Call, Events } from "@wailsio/runtime";
import "../styles/menu.css";

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
  return pageSheet(title, message, buttons);
}

/** In a browser: a menu drawn by the page. ↑↓ move, Return chooses, Escape
 *  or a click elsewhere closes it, as an NSMenu. */
function pageMenu(entries: MenuEntry[], x: number, y: number) {
  closePageMenu();
  const menu = document.createElement("div");
  menu.className = "page-menu";
  menu.tabIndex = -1;
  const rows: { el: HTMLElement; entry: MenuEntry }[] = [];
  let active = -1;
  const highlight = (i: number) => {
    rows[active]?.el.classList.remove("active");
    active = i;
    rows[active]?.el.classList.add("active");
  };
  const choose = (entry: MenuEntry) => {
    closePageMenu();
    entry.action?.();
  };
  for (const entry of entries) {
    const row = document.createElement("div");
    if (entry.separator) {
      row.className = "page-menu-rule";
    } else if (entry.header) {
      row.className = "page-menu-header";
      row.textContent = entry.label ?? "";
    } else {
      row.className = `page-menu-item${entry.disabled ? " disabled" : ""}`;
      if (entry.checked) {
        const check = document.createElement("span");
        check.className = "page-menu-check";
        check.textContent = "✓";
        row.appendChild(check);
      }
      row.appendChild(document.createTextNode(entry.label ?? ""));
      if (!entry.disabled) {
        const index = rows.length;
        rows.push({ el: row, entry });
        row.onmouseenter = () => highlight(index);
        row.onmouseleave = () => highlight(-1);
        row.onmouseup = () => choose(entry);
      }
    }
    menu.appendChild(row);
  }
  document.body.appendChild(menu);
  // Within the window: flipped up or left where it would run off it.
  const box = menu.getBoundingClientRect();
  const left = x + box.width > innerWidth - 4 ? Math.max(4, x - box.width) : x;
  const top = y + box.height > innerHeight - 4 ? Math.max(4, y - box.height) : y;
  Object.assign(menu.style, { left: `${left}px`, top: `${top}px` });
  menu.focus();

  const onKey = (e: KeyboardEvent) => {
    if (e.key === "Escape") closePageMenu();
    else if (e.key === "ArrowDown") highlight(rows.length ? (active + 1) % rows.length : -1);
    else if (e.key === "ArrowUp") highlight(rows.length ? (active <= 0 ? rows.length - 1 : active - 1) : -1);
    else if (e.key === "Enter" && rows[active]) choose(rows[active].entry);
    else return;
    e.preventDefault();
    e.stopPropagation();
  };
  const onDown = (e: MouseEvent) => {
    if (!menu.contains(e.target as Node)) closePageMenu();
  };
  document.addEventListener("keydown", onKey, true);
  window.addEventListener("blur", closePageMenu);
  setTimeout(() => document.addEventListener("mousedown", onDown, true), 0);
  closeOpenMenu = () => {
    document.removeEventListener("keydown", onKey, true);
    document.removeEventListener("mousedown", onDown, true);
    window.removeEventListener("blur", closePageMenu);
    menu.remove();
  };
}

let closeOpenMenu: (() => void) | null = null;

function closePageMenu() {
  const close = closeOpenMenu;
  closeOpenMenu = null;
  close?.();
}

/** In a browser: an alert sheet drawn by the page. Return presses the first
 *  button, Escape the last (Cancel), as an NSAlert. */
function pageSheet(title: string, message: string, buttons: string[]): Promise<string> {
  return new Promise((resolve) => {
    const shade = document.createElement("div");
    shade.className = "page-sheet-shade";
    const sheet = document.createElement("div");
    sheet.className = "page-sheet";
    sheet.setAttribute("role", "alertdialog");
    const icon = document.createElement("img");
    icon.src = "/assets/agentman.png";
    icon.alt = "";
    const heading = document.createElement("div");
    heading.className = "page-sheet-title";
    heading.textContent = title;
    sheet.append(icon, heading);
    if (message) {
      const body = document.createElement("div");
      body.className = "page-sheet-message";
      body.textContent = message;
      sheet.appendChild(body);
    }
    const row = document.createElement("div");
    row.className = `page-sheet-buttons${buttons.length === 2 ? " two" : ""}`;
    const answer = (label: string) => {
      document.removeEventListener("keydown", onKey, true);
      shade.remove();
      resolve(label);
    };
    // Two buttons sit side by side with Cancel first, as macOS lays them out.
    const order = buttons.length === 2 ? [buttons[1], buttons[0]] : buttons;
    for (const label of order) {
      const button = document.createElement("button");
      button.textContent = label;
      if (label === buttons[0]) button.className = "default";
      button.onclick = () => answer(label);
      row.appendChild(button);
    }
    sheet.appendChild(row);
    shade.appendChild(sheet);
    document.body.appendChild(shade);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Enter") answer(buttons[0]);
      else if (e.key === "Escape") answer(buttons[buttons.length - 1]);
      else return;
      e.preventDefault();
      e.stopPropagation();
    };
    document.addEventListener("keydown", onKey, true);
  });
}
