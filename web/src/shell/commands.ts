// The menu bar's items for the main window (AgentmanApp.swift's commands),
// and the launch options a test or screenshot opens a screen with
// (ContentView.swift's AGENTMAN_* environment; here AGENTMAN_OPEN's query).
// In a plain browser, which has no menu bar, the page takes the menu's
// shortcuts itself.

import { openLightbox } from "../lightbox";
import { getPref, setPref } from "../prefs";
import { openAgentman, openLink, openNewAgent } from "../nav";
import { showAwaySince, showLastAway } from "../away/away";
import { cachedHome, onCommand, type Command } from "./native";
import { setShell } from "./state";
import { hostCall, openEditor, openSettings, pickFolder } from "../host";

const inShell = location.protocol === "wails:" || location.hostname === "wails.localhost";

export function run(command: Command) {
  switch (command) {
    case "new-agent":
      openNewAgent();
      break;
    case "toggle-sidebar":
      setPref("sidebarHidden", !getPref("sidebarHidden", false));
      break;
    case "cycle-appearance": {
      // Light, Dark, Match the Mac, in turn.
      const order = ["light", "dark", "system"];
      const now = getPref("appearance", "system");
      setPref("appearance", order[((order.indexOf(now) < 0 ? 2 : order.indexOf(now)) + 1) % order.length]);
      break;
    }
    case "palette":
      setShell({ showPalette: true });
      break;
    case "agentman":
      openAgentman();
      break;
    case "away":
      showLastAway();
      break;
    // The menu bar's own in the shell (Settings…, Open Folder…); the
    // page's in the browser.
    case "settings":
      void openSettings();
      break;
    case "open-folder":
      void pickFolder("Choose a folder to open in the editor", cachedHome()).then((folder) => {
        if (folder) void openEditor({ folder });
      });
      break;
  }
}

/** The menu's shortcuts, for the page in a browser, which has no menu bar. */
function shortcut(e: KeyboardEvent): Command | null {
  const mod = e.metaKey || e.ctrlKey;
  const key = e.key.toLowerCase();
  if (mod && !e.altKey && !e.shiftKey && key === "n") return "new-agent";
  if (mod && !e.altKey && !e.shiftKey && key === "k") return "palette";
  if (mod && !e.altKey && !e.shiftKey && key === ",") return "settings";
  if (mod && !e.altKey && !e.shiftKey && key === "o") return "open-folder";
  if (e.ctrlKey && (e.metaKey || e.altKey) && key === "s") return "toggle-sidebar";
  if (mod && e.altKey && e.shiftKey && (key === "a" || e.code === "KeyA")) return "cycle-appearance";
  if (mod && e.altKey && !e.shiftKey && (key === "0" || e.code === "Digit0")) return "agentman";
  return null;
}

let started = false;

/** Listens for the menu's items and reads the launch options; once. */
export function startShellCommands(): () => void {
  if (started) return () => {};
  started = true;
  const off = onCommand(run);
  const keys = (e: KeyboardEvent) => {
    // In the app the menu bar has them (macOS's, or the window's on Linux).
    if (inShell) return;
    const command = shortcut(e);
    if (!command) return;
    e.preventDefault();
    run(command);
  };
  window.addEventListener("keydown", keys);
  launchOptions();
  return () => {
    off();
    window.removeEventListener("keydown", keys);
  };
}

/** For screenshots and tests: open=<agentman:// link>, folder=<path> (the
 *  folder filter), renaming=<session id>, away=<minutes> (the summary since
 *  then, after 4 s), palette=<query> (the palette open, typed into),
 *  lightbox=<image path> (the image open full window). */
function launchOptions() {
  const q = new URLSearchParams(location.search);
  const link = q.get("open");
  if (link) openLink(link);
  const folder = q.get("folder");
  if (folder) setShell({ folderFilter: folder });
  const renaming = q.get("renaming");
  if (renaming) setShell({ renaming });
  const away = Number(q.get("away"));
  if (q.has("away") && away > 0) window.setTimeout(() => showAwaySince(Date.now() - away * 60_000), 4000);
  if (q.has("palette")) setShell({ showPalette: true });
  const image = q.get("lightbox");
  if (image && inShell) {
    void hostCall("Files.Read", image, 50 * 1024 * 1024).then((file) => {
      const f = file as { data: string; mime: string; name: string };
      if (f.data) openLightbox([{ src: `data:${f.mime};base64,${f.data}`, title: f.name, path: image }]);
    }).catch(() => {});
  }
}
