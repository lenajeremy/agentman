import type { TextStyle } from "react-native";

/**
 * Design tokens.
 *
 * The app answers one question: "what are my agents doing, and does one of
 * them need me?" Colour carries that answer, so each state owns one hue and
 * nothing decorative competes with it:
 *
 *   working      — cobalt, which is also the brand
 *   needs you    — tangerine, the one colour that asks for a tap
 *   idle         — no colour at all; nothing is happening, so nothing glows
 *
 * The ground is a warm off-white rather than pure white, so white cards read as
 * objects without needing heavy borders. Cards are separated by fill and a
 * hairline, not by shadows stacked on shadows.
 *
 * Light is the default. Dark is the same system inverted, with the state
 * colours lifted so they keep their contrast on a dark ground.
 */
const light = {
  /** Page background. */
  paper: "#F5F5F2",
  /** Cards and sheets. */
  surface: "#FFFFFF",
  /** Tiles, code, secondary controls. */
  fill: "#EDECE8",
  /** Borders on controls, pressed fills. */
  fillStrong: "#E4E3DE",
  /** Hairlines and card edges. */
  line: "#E3E2DD",
  /** A tool call's soft card, a step greyer than the page, as on the Mac;
   *  its edge; and the well its output sits in. */
  toolCard: "#EEEEEA",
  toolCardEdge: "#E5E4DF",
  toolWell: "#F8F8F5",

  text: "#0F1012",
  /** Body copy that should not compete with titles. */
  textSecondary: "#34363B",
  /** Secondary text: paths, timestamps, tool summaries. */
  muted: "#6A6D75",
  /** Tertiary: labels, placeholders, counts. */
  faint: "#9C9EA5",

  /** Primary buttons are ink pills; this is their fill and label. */
  inverse: "#0F1012",
  onInverse: "#FFFFFF",
  /** An accent that stays legible on the inverse fill (the undo bar). */
  inverseAccent: "#9DAAFF",

  /** An agent is working. Also the brand colour. */
  working: "#2E4BFF",
  workingText: "#1F35C7",
  workingWash: "#EEF1FF",
  workingSoft: "#D9DFFF",
  /** An agent is blocked on the user — the most actionable state there is. */
  needsYou: "#FF6A2B",
  needsYouText: "#C2451A",
  needsYouWash: "#FFF2EB",
  needsYouEdge: "#FFDCCB",
  /** Delivery confirmed, Mac online. */
  ok: "#12A66A",
  okWash: "#E6F6EE",
  /** A tool call failed, the Mac is offline. */
  error: "#E5484D",
  errorText: "#C4343A",
  errorWash: "#FDEDEE",
  errorEdge: "#F6C9CB",
  /** A count on a solid state colour — the review badge. White in both
   *  themes, because the state colours are saturated in both. */
  onAccent: "#FFFFFF",

  /** Dims the page behind a sheet. */
  scrim: "rgba(15, 16, 18, 0.32)",
  /** Controls drawn over the camera, which is dark whatever the theme. */
  cameraScrim: "rgba(10, 11, 13, 0.62)",
  shadow: "#0F1012",
};

export type Palette = { [K in keyof typeof light]: string };

const dark: Palette = {
  paper: "#0E0F11",
  surface: "#17181B",
  fill: "#202226",
  fillStrong: "#2C2E33",
  line: "#25272C",
  toolCard: "#17181B",
  toolCardEdge: "#212327",
  toolWell: "#0F1012",

  text: "#F2F3F5",
  textSecondary: "#D2D4D9",
  muted: "#9A9DA5",
  faint: "#6E717A",

  inverse: "#F2F3F5",
  onInverse: "#0E0F11",
  inverseAccent: "#2E4BFF",

  working: "#6B80FF",
  workingText: "#9DAAFF",
  workingWash: "#1A1F3D",
  workingSoft: "#28305E",
  needsYou: "#FF7D45",
  needsYouText: "#FF9A6C",
  needsYouWash: "#2E1B12",
  needsYouEdge: "#4A2818",
  ok: "#2BC784",
  okWash: "#10271D",
  error: "#FF6B70",
  errorText: "#FF8A8E",
  errorWash: "#2C1517",
  errorEdge: "#4D2226",
  onAccent: "#FFFFFF",

  scrim: "rgba(0, 0, 0, 0.5)",
  cameraScrim: "rgba(10, 11, 13, 0.62)",
  shadow: "#000000",
};

export const palettes: Record<"light" | "dark", Palette> = { light, dark };

/**
 * Google Sans for everything a person wrote or reads as interface; Google
 * Sans Code for machine-authored text, as on the Mac. Paths, session ids,
 * shell commands and agent names are strings a program produced, and setting
 * them in mono says so at a glance.
 *
 * Each face is a style fragment, spread into a style (`...font.sansMedium`),
 * so a later style that sets another face replaces it whole.
 *
 * Italics are faces of their own. iOS does not slant a custom font for
 * fontStyle: "italic", so emphasis set that way rendered upright.
 */
export const font = {
  sans: { fontFamily: "GoogleSans-Regular" },
  sansItalic: { fontFamily: "GoogleSans-Italic" },
  sansMedium: { fontFamily: "GoogleSans-Medium" },
  sansBold: { fontFamily: "GoogleSans-SemiBold" },
  sansBoldItalic: { fontFamily: "GoogleSans-SemiBoldItalic" },
  mono: { fontFamily: "GoogleSansCode-Regular" },
  monoMedium: { fontFamily: "GoogleSansCode-Medium" },
} as const satisfies Record<string, TextStyle>;

/**
 * Every text size in the app is this much larger than it is written: the
 * user found the text too small to read comfortably. One number moves it all,
 * the sizes below and every size written into a screen (through scaled).
 */
export const textScale = 1.1;

/** A text size or line height as the app draws it, to the half point. */
export function scaled(points: number): number {
  return Math.round(points * textScale * 2) / 2;
}

export const size = {
  /** Section labels, counts. */
  label: scaled(12),
  caption: scaled(13),
  body: scaled(15),
  /** What a conversation says: a touch smaller than body, on a looser line,
   *  for reading paragraphs rather than scanning rows. */
  reading: scaled(14.5),
  title: scaled(17),
  heading: scaled(22),
  display: scaled(34),
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
  xxl: 24,
  sheet: 28,
  pill: 999,
} as const;

/** Shared responsive constraints keep tablet layouts readable and centred. */
export const layout = {
  contentMax: 760,
  formMax: 540,
  touchTarget: 44,
} as const;

/** Per-state presentation, kept in one place so the list and detail agree. */
export function stateStyle(
  state: string,
  c: Palette,
): {
  color: string;
  /** A readable text colour for the label on the page background. */
  text: string;
  wash: string;
  label: string;
  /** Priority for grouping; lower sorts first. */
  rank: number;
} {
  switch (state) {
    case "waiting_input":
      return { color: c.needsYou, text: c.needsYouText, wash: c.needsYouWash, label: "Needs you", rank: 0 };
    case "busy":
      return { color: c.working, text: c.workingText, wash: c.workingWash, label: "Working", rank: 1 };
    case "idle":
      return { color: c.faint, text: c.muted, wash: c.fill, label: "Idle", rank: 2 };
    default:
      return { color: c.faint, text: c.muted, wash: c.fill, label: "Ended", rank: 3 };
  }
}

/** Short, recognisable names for each CLI, used where a whole word won't fit. */
export function agentLabel(kind: string): { name: string; short: string } {
  switch (kind) {
    case "claude":
      return { name: "Claude Code", short: "cc" };
    case "codex":
      return { name: "Codex", short: "cx" };
    case "opencode":
      return { name: "OpenCode", short: "oc" };
    case "cursor":
      return { name: "Cursor", short: "cu" };
    case "cursor-cli":
      return { name: "Cursor CLI", short: "cu" };
    case "kiro":
      return { name: "Kiro", short: "ki" };
    case "antigravity":
      return { name: "Antigravity", short: "ag" };
    default:
      return { name: kind, short: kind.slice(0, 2) };
  }
}

/** Collapse $HOME so a path fits on a phone without losing its meaning. */
export function shortPath(path: string): string {
  const parts = path.split("/").filter(Boolean);
  if (parts.length <= 2) return path;
  return "~/" + parts.slice(-2).join("/");
}

/**
 * Relative time, tuned for glanceability rather than precision.
 *
 * Past a month it turns into a date. "412d" is a number nobody converts; "4
 * Aug" is a day you might remember working. The year is added only when it is
 * not this one, so the common case stays short.
 */
export function ago(epochMillis: number, now = Date.now()): string {
  if (!epochMillis) return "";
  const seconds = Math.max(0, Math.floor((now - epochMillis) / 1000));
  if (seconds < 10) return "now";
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  if (days <= 30) return `${days}d`;
  return onDate(new Date(epochMillis), new Date(now));
}

const months = [
  "Jan", "Feb", "Mar", "Apr", "May", "Jun",
  "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
] as const;

function onDate(when: Date, now: Date): string {
  const day = `${when.getDate()} ${months[when.getMonth()]}`;
  return when.getFullYear() === now.getFullYear()
    ? day
    : `${day} ${String(when.getFullYear() % 100).padStart(2, "0")}`;
}
