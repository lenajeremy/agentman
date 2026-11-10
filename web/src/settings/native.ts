// What the Settings window asks the shell for (settings.go): the home
// folder and open panels. In a plain browser, the nearest a page can do.

import { useEffect, useState } from "react";
import { Call, Clipboard } from "@wailsio/runtime";
import { hostCall } from "../host";

const inShell = location.protocol === "wails:" || location.hostname === "wails.localhost";

let homeCache = "";
const homeReady = inShell ? hostCall("Settings.Home").then((h) => (homeCache = String(h ?? ""))).catch(() => "") : Promise.resolve("");

/** The user's home folder, for ~ in paths ("" until known). */
export function useHome(): string {
  const [home, setHome] = useState(homeCache);
  useEffect(() => {
    void homeReady.then(() => setHome(homeCache));
  }, []);
  return home;
}

export function home(): string {
  return homeCache;
}

export interface PickOptions {
  folders: boolean;
  multiple?: boolean;
  message?: string;
  button?: string;
  start?: string;
}

/** An open panel; nothing when cancelled. */
export async function pick(opts: PickOptions): Promise<string[]> {
  if (!inShell) {
    const path = prompt(opts.message || (opts.folders ? "Folder" : "File"), opts.start ?? "");
    return path ? [path] : [];
  }
  try {
    const paths = (await Call.ByName("main.Settings.Pick", { multiple: false, message: "", button: "", start: "", ...opts })) as string[] | null;
    return paths ?? [];
  } catch {
    return [];
  }
}

/** Puts text on the pasteboard. */
export async function copyText(text: string): Promise<void> {
  if (inShell) {
    await Clipboard.SetText(text);
    return;
  }
  await navigator.clipboard?.writeText(text);
}
