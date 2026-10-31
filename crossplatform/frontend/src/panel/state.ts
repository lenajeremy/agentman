// The right panel's and the terminal's kept state (Views/RightPanel.swift's
// statics, AgentThreadView.swift's @AppStorage), under the Mac's own keys,
// so every view keeping them follows and both apps are easy to compare.

import { getPref, setPref, usePref } from "../prefs";

export type PanelTab = "diff" | "files" | "history" | "terminal";
export type TerminalDock = "bottom" | "side";

export const PANEL_TABS: PanelTab[] = ["diff", "files", "history", "terminal"];

export const tabTitle: Record<PanelTab, string> = { diff: "Diff", files: "Files", history: "History", terminal: "Terminal" };
export const tabSymbol: Record<PanelTab, string> = {
  diff: "doc.text", files: "folder", history: "clock.arrow.circlepath", terminal: "terminal",
};

export const OPEN_KEY = "rightPanelOpen";
export const TAB_KEY = "rightPanelTab";
export const TABS_KEY = "rightPanelTabs";
export const WIDTH_KEY = "rightPanelWidth";
export const DOCK_KEY = "terminalDock";
export const BOTTOM_KEY = "bottomTerminalOpen";
export const BOTTOM_HEIGHT_KEY = "bottomTerminalHeight";

export const DEFAULT_WIDTH = 540;
export const MIN_WIDTH = 360;
/** The least the conversation keeps beside the right panel. */
export const THREAD_ROOM = 380;
/** The workspace card's width beside the thread, and with something open. */
export const CARD_WIDTH = 220;
export const CARD_OPEN_WIDTH = 540;

// For comparisons, as the Mac's defaults stand in for them: ?panel=history
// opens the panel on a tab, ?dock=side keeps the terminal in it.
const query = new URLSearchParams(location.search);
if (query.get("panel")) {
  const tab = query.get("panel") as PanelTab;
  if (PANEL_TABS.includes(tab)) {
    setPref(OPEN_KEY, true);
    setPref(TAB_KEY, tab);
    const tabs = parseTabs(query.get("tabs") ?? getPref(TABS_KEY, "diff"));
    if (!tabs.includes(tab)) tabs.push(tab);
    setPref(TABS_KEY, tabs.join(","));
  }
}
if (query.get("dock") === "side" || query.get("dock") === "bottom") setPref(DOCK_KEY, query.get("dock"));
// ?prefs=diffSplit:true,diffWrap:false, as MAC_DEFAULTS sets the Mac's.
for (const pair of (query.get("prefs") ?? "").split(",").filter(Boolean)) {
  const [key, raw] = pair.split(":");
  try {
    setPref(key, JSON.parse(raw));
  } catch {
    setPref(key, raw);
  }
}

/** The tabs open, in order. */
export function parseTabs(raw: string): PanelTab[] {
  return raw.split(",").filter((t): t is PanelTab => PANEL_TABS.includes(t as PanelTab));
}

export function usePanelTabs(): [PanelTab[], (tabs: PanelTab[]) => void] {
  const [raw, setRaw] = usePref(TABS_KEY, "diff");
  return [parseTabs(raw), (tabs) => setRaw(tabs.join(","))];
}

/** Opens the panel on a tab, adding the tab if it was closed. */
export function showPanel(tab: PanelTab) {
  const tabs = parseTabs(getPref(TABS_KEY, "diff"));
  if (!tabs.includes(tab)) tabs.push(tab);
  setPref(TABS_KEY, tabs.join(","));
  setPref(TAB_KEY, tab);
  setPref(OPEN_KEY, true);
}

export function closePanel() {
  setPref(OPEN_KEY, false);
}

/** Moves the terminal, and keeps it open there. It shows in one place at a time. */
export function dockTerminal(dock: TerminalDock) {
  setPref(DOCK_KEY, dock);
  if (dock === "side") {
    setPref(BOTTOM_KEY, false);
    showPanel("terminal");
    return;
  }
  let tabs = parseTabs(getPref(TABS_KEY, "diff")).filter((t) => t !== "terminal");
  if (!tabs.length) {
    setPref(OPEN_KEY, false);
    tabs = ["diff"];
  }
  setPref(TABS_KEY, tabs.join(","));
  if (getPref<PanelTab>(TAB_KEY, "diff") === "terminal") setPref(TAB_KEY, tabs[0]);
  setPref(BOTTOM_KEY, true);
}

/** Opens a terminal where the user keeps it: under the conversation unless
 *  they moved it into the panel. Starting a shell is the caller's (ensureShell). */
export function showTerminalWhereKept() {
  if (getPref<TerminalDock>(DOCK_KEY, "bottom") === "side") showPanel("terminal");
  else dockTerminal("bottom");
}

/** The panel's width in a window this wide: what it was dragged to, but never
 *  so wide the conversation has under `room` left. */
export function clampWidth(width: number, available: number, room = THREAD_ROOM): number {
  return Math.min(Math.max(width, MIN_WIDTH), Math.max(MIN_WIDTH, available - room));
}
