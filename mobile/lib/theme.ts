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
 * The base is a deep blue-black rather than pure black: agents run at night,
 * on a phone, and #000 on OLED next to coloured state reads as a hole in the
 * screen instead of a surface.
 */
export const color = {
  /** Page background. */
  ink: "#090C12",
  /** Raised surface — cards, the composer bar. */
  surface: "#131823",
  /** A surface that needs to read independently from the page. */
  surfaceRaised: "#181E2A",
  /** A pressed or nested surface. */
  sunken: "#0D1119",
  /** Hairlines and dividers. */
  line: "#252C3A",
  /** A stronger divider, for section boundaries. */
  lineStrong: "#364055",

  text: "#F0F3F9",
  /** Secondary text: paths, timestamps, tool summaries. */
  muted: "#9AA5B8",
  /** Tertiary: labels, section headers. */
  faint: "#657086",

  /** An agent is working. */
  working: "#63D0FF",
  workingWash: "#102431",
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
