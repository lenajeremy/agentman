// The terminal's look (Terminal/TerminalSession.swift's AgentmanTerminalView
// applyTheme): its own white or black page, the 16 ANSI colours, a blue
// caret, Google Sans Code at 12.5.

import type { ITheme } from "@xterm/xterm";

const LIGHT_ANSI = [
  "#1E2025", "#C4343A", "#0E8A57", "#A8690F", "#1F35C7", "#8E3BB8", "#0F7C8C", "#6A6D75",
  "#9C9EA5", "#E5484D", "#12A66A", "#B7791F", "#2E4BFF", "#A855D6", "#1B98AB", "#34363B",
];
const DARK_ANSI = [
  "#2A2C31", "#FF6B70", "#2BC784", "#E2B45C", "#6B80FF", "#D19AF5", "#5CC8D8", "#D2D4D9",
  "#6E717A", "#FF8A8E", "#4FDB9C", "#F2C879", "#9DAAFF", "#E0B8FF", "#7FDCEA", "#F2F3F5",
];
const NAMES = [
  "black", "red", "green", "yellow", "blue", "magenta", "cyan", "white",
  "brightBlack", "brightRed", "brightGreen", "brightYellow", "brightBlue", "brightMagenta", "brightCyan", "brightWhite",
] as const;

export const TERMINAL_FONT = '"Google Sans Code", ui-monospace, Menlo, Consolas, monospace';
export const TERMINAL_SIZE = 12.5;

export function isDark(): boolean {
  const forced = document.documentElement.dataset.theme;
  if (forced === "dark") return true;
  if (forced === "light") return false;
  return window.matchMedia("(prefers-color-scheme: dark)").matches;
}

export function terminalTheme(dark = isDark()): ITheme {
  const ansi = dark ? DARK_ANSI : LIGHT_ANSI;
  const caret = dark ? "#6B80FF" : "#2E4BFF";
  const theme: ITheme = {
    background: dark ? "#000000" : "#FFFFFF",
    foreground: dark ? "#E6E7EA" : "#1E2025",
    cursor: caret,
    cursorAccent: dark ? "#000000" : "#FFFFFF",
    selectionBackground: caret + "4D",
  };
  NAMES.forEach((name, i) => ((theme as Record<string, string>)[name] = ansi[i]));
  return theme;
}

/** Calls back when the system (or the app's setting) turns light or dark. */
export function onAppearance(callback: () => void): () => void {
  const media = window.matchMedia("(prefers-color-scheme: dark)");
  media.addEventListener("change", callback);
  const observer = new MutationObserver(callback);
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  return () => {
    media.removeEventListener("change", callback);
    observer.disconnect();
  };
}
