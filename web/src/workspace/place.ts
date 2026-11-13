// What the Mac app calls "this Mac", said for the computer the app is on,
// and its file manager's name. In a browser that is the computer am serve
// runs on, whose folders these are and where Reveal shows them.

import { knownSystem } from "../host";

function os(): string {
  const platform = document.documentElement.dataset.platform ?? "";
  return platform === "browser" ? knownSystem() ?? "" : platform;
}

/** "This Mac" on a Mac, else "This computer". */
export function placeTitle(): string {
  return os() === "darwin" ? "This Mac" : "This computer";
}

/** "this Mac", in the middle of a sentence. */
export function thisComputer(): string {
  return os() === "darwin" ? "this Mac" : "this computer";
}

/** Finder on a Mac, else the file manager. */
export function fileManager(): string {
  return os() === "darwin" ? "Finder" : "Files";
}
