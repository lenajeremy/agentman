/**
 * Design tokens.
 *
 * This is a status board, not a chat app. Its one job is to answer "what are
 * my agents doing, and does one of them need me?" — so the visual system is
 * built around that: a near-monochrome base where colour is earned only by
 * state, never spent on decoration.
 *
 * Three states matter, and each gets exactly one colour:
 *   working      — cyan, reads as instrumentation rather than a success tick
 *   needs you    — amber, the borrowed vocabulary of a real control panel
 *   idle         — no colour at all; nothing is happening, so nothing glows
 *
 * The base is a neutral near-black. A hair off #000 rather than pure, so an
 * OLED panel has a surface to render instead of switching pixels off entirely
 * beside lit colour — but neutral, not tinted, so the state colours are the
 * only hue on screen.
 *
 * A card is drawn by its border, not by being a paler rectangle. Filling every
 * surface produces a stack of grey slabs; outlining them keeps the ground
 * continuous and lets the border carry the structure.
 */
export const color = {
  /** Page background. */
  ink: "#08080A",
  /** A card. Barely lifted: on this ground a card is defined by its border,
   *  not by being a paler rectangle. */
  surface: "#101012",
  /** Controls — buttons, pills, segmented tracks. These do read as filled. */
  surfaceRaised: "#1C1C20",
  /** A pressed or nested surface. */
  sunken: "#0B0B0D",
  /** Hairlines and dividers. */
  line: "#2A2A2E",
  /** A stronger divider, for section boundaries. */
  lineStrong: "#3A3A41",

  text: "#F0F3F9",
  /** Secondary text: paths, timestamps, tool summaries. */
  muted: "#9AA5B8",
  /** Tertiary: labels, section headers. */
  faint: "#657086",

  /** An agent is working. */
  working: "#6AAECD",
  workingWash: "#141F27",
  /** An agent is blocked on the user — the most actionable state there is. */
  needsYou: "#FFB84A",
  needsYouWash: "#271F12",
  /** A tool call failed. */
  error: "#FF747A",
  errorWash: "#29171B",
  /** Delivery confirmed. */
  ok: "#52DFA3",
  okWash: "#12251E",
  /** Camera and modal chrome over unpredictable content. */
  scrim: "rgba(5, 8, 13, 0.76)",
} as const;

/**
 * Monospace is not styling here — it marks machine-authored text. Paths,
 * session ids, shell commands, and agent names are all strings a program
 * produced, and setting them in mono tells you that at a glance. Prose the
 * model or the user wrote is set in the sans face.
 */
export const font = {
  sans: "IBMPlexSans_400Regular",
  sansMedium: "IBMPlexSans_500Medium",
  sansBold: "IBMPlexSans_600SemiBold",
  mono: "IBMPlexMono_400Regular",
  monoMedium: "IBMPlexMono_500Medium",
} as const;

export const size = {
  /** Section labels. */
  label: 11,
  caption: 12,
  body: 15,
  title: 18,
  heading: 22,
  display: 30,
} as const;

export const space = {
  xxs: 2,
  xs: 4,
  sm: 8,
  md: 12,
  lg: 16,
  xl: 24,
  xxl: 40,
  xxxl: 56,
} as const;

export const radius = {
  sm: 8,
  md: 12,
  lg: 16,
  xl: 20,
  /** Cards and rows. The reference leans on generous rounding to read modern. */
  xxl: 24,
  pill: 999,
} as const;

/** Shared responsive constraints keep tablet layouts readable and centred. */
export const layout = {
  contentMax: 760,
  formMax: 540,
  touchTarget: 44,
} as const;

/** Per-state presentation, kept in one place so the list and detail agree. */
export function stateStyle(state: string): {
  color: string;
  label: string;
  /** Priority for grouping; lower sorts first. */
  rank: number;
} {
  switch (state) {
    case "waiting_input":
      return { color: color.needsYou, label: "Needs you", rank: 0 };
    case "busy":
      return { color: color.working, label: "Working", rank: 1 };
    case "idle":
      return { color: color.faint, label: "Idle", rank: 2 };
    default:
      return { color: color.faint, label: "Ended", rank: 3 };
  }
}

/** Collapse $HOME so a path fits on a phone without losing its meaning. */
export function shortPath(path: string): string {
  const parts = path.split("/").filter(Boolean);
  if (parts.length <= 2) return path;
  return "~/" + parts.slice(-2).join("/");
}

/** Relative time, tuned for glanceability rather than precision. */
export function ago(epochMillis: number): string {
  if (!epochMillis) return "";
  const seconds = Math.max(0, Math.floor((Date.now() - epochMillis) / 1000));
  if (seconds < 10) return "now";
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}
