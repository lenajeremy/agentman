// The editor's menu commands (EditorWindow.swift's EditorCommands), run
// from the keyboard here and from the menu bar's items (editormenu.go,
// through the "agentman:editor-command" event). Keys the editor page
// doesn't keep come out of it through scripts/host-shim.js.

import { Events, Window } from "@wailsio/runtime";
import { getPref, setPref } from "../prefs";
import { openEditor, pickFolder } from "../host";
import { inShell } from "./fs";
import type { EditorStore } from "./store";
import { MAX_PANES } from "./store";

export type Command =
  | "save" | "saveAll" | "revert"
  | "openFolder" | "goToFile" | "searchInFolder" | "toggleAgentPanel"
  | "toggleTerminal" | "newTerminal" | "clearTerminal" | "closeTerminal"
  | "togglePreview" | "split" | "closeSplit"
  | "goToDefinition" | "findReferences" | "renameSymbol" | "formatDocument" | "goToSymbol" | "nextProblem"
  | "commentLines" | "indentLines" | "outdentLines"
  | "toggleVim" | "biggerText" | "smallerText" | "actualSize"
  | "closeTab";

const MONACO: Partial<Record<Command, string>> = {
  goToDefinition: "editor.action.revealDefinition",
  findReferences: "editor.action.goToReferences",
  renameSymbol: "editor.action.rename",
  formatDocument: "editor.action.formatDocument",
  goToSymbol: "editor.action.quickOutline",
  nextProblem: "editor.action.marker.next",
  commentLines: "editor.action.commentLine",
  indentLines: "editor.action.indentLines",
  outdentLines: "editor.action.outdentLines",
};

/** Whether a command can run now, as the menu item's disabled state. */
export function enabled(store: EditorStore, command: Command): boolean {
  switch (command) {
    case "togglePreview":
      return !!store.active?.isMarkdown;
    case "split":
      return store.panes.length < MAX_PANES;
    case "closeSplit":
      return store.panes.length >= 2;
    default:
      return true;
  }
}

export async function runCommand(store: EditorStore, command: Command) {
  if (!enabled(store, command)) return;
  const action = MONACO[command];
  if (action) {
    store.monaco?.run(action);
    return;
  }
  switch (command) {
    case "save":
      await store.saveActive();
      break;
    case "saveAll":
      await store.saveAll();
      break;
    case "revert":
      store.active?.revert();
      break;
    case "openFolder": {
      const folder = await pickFolder("Choose a folder to open in the editor", store.home);
      if (folder) await openEditor({ folder });
      break;
    }
    case "goToFile":
      store.searchOpen = false;
      store.quickOpen = true;
      store.changed();
      break;
    case "searchInFolder":
      store.showSearch();
      break;
    case "toggleAgentPanel":
      store.setShowAgentPanel(!store.showAgentPanel);
      break;
    case "toggleTerminal":
      store.toggleTerminal();
      break;
    case "newTerminal":
      store.newTerminal();
      break;
    case "clearTerminal":
      window.dispatchEvent(new CustomEvent("agentman:terminal-clear"));
      break;
    case "closeTerminal":
      store.showTerminal = false;
      store.terminalGeneration += 1;
      store.changed();
      break;
    case "togglePreview":
      store.togglePreview();
      break;
    case "split":
      store.split();
      break;
    case "closeSplit":
      store.closePane(store.focusedPane);
      break;
    case "toggleVim":
      setPref("editorVim", !getPref("editorVim", false));
      break;
    case "biggerText":
      adjustFont(1);
      break;
    case "smallerText":
      adjustFont(-1);
      break;
    case "actualSize":
      setPref("editorFontSize", 14);
      break;
    case "closeTab": {
      // An agent's Diff or History in front closes first.
      if (store.workspaceTab) {
        store.closeWorkspace(store.workspaceTab);
        break;
      }
      const document = store.active;
      if (document) store.requestClose(document);
      else if (inShell) void Window.Close();
      else window.close();
      break;
    }
  }
}

function adjustFont(step: number) {
  setPref("editorFontSize", Math.min(Math.max(getPref("editorFontSize", 14) + step, 9), 28));
}

const mac = /Mac/.test(navigator.platform);

interface Keys {
  key: string;
  code?: string;
  metaKey: boolean;
  ctrlKey: boolean;
  shiftKey: boolean;
  altKey: boolean;
}

/** The command a key press is, as the menus' shortcuts are; off the Mac,
 *  Ctrl stands for ⌘. */
export function commandFor(e: Keys): Command | null {
  const key = e.key.length === 1 ? e.key.toLowerCase() : e.key;
  const command = mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey;
  if (e.ctrlKey && !e.metaKey && !e.altKey && (key === "`" || key === "~" || e.code === "Backquote")) {
    return e.shiftKey ? "newTerminal" : "toggleTerminal";
  }
  if (!e.metaKey && !e.ctrlKey) {
    if (key === "F12") return e.shiftKey ? "findReferences" : "goToDefinition";
    if (key === "F2" && !e.shiftKey && !e.altKey) return "renameSymbol";
    if (key === "F8" && !e.shiftKey && !e.altKey) return "nextProblem";
    if (e.altKey && e.shiftKey && (key === "f" || key === "ƒ" || e.code === "KeyF")) return "formatDocument";
    return null;
  }
  if (!command) return null;
  if (e.shiftKey && !e.altKey) {
    if (key === "f") return "searchInFolder";
    if (key === "v") return "togglePreview";
    if (key === "o") return "goToSymbol";
    if (key === "+") return "biggerText";
    return null;
  }
  if (e.altKey && !e.shiftKey && (key === "s" || key === "ß" || e.code === "KeyS")) return "saveAll";
  if (e.altKey) return null;
  switch (key) {
    case "s": return "save";
    case "o": return "openFolder";
    case "p": return "goToFile";
    case "l": return "toggleAgentPanel";
    case "\\": return "split";
    case "=": case "+": return "biggerText";
    case "-": return "smallerText";
    case "0": return "actualSize";
    case "w": return "closeTab";
    default: return null;
  }
}

/** The menu bar's items, sent by the shell to the window in front. */
export function onMenuCommand(handler: (command: Command) => void): () => void {
  if (!inShell) return () => {};
  return Events.On("agentman:editor-command", (event) => handler(String(event.data) as Command));
}
