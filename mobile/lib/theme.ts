import type { TextStyle } from "react-native";

/**
 * Design tokens, the Mac app's (desktop/Agentman/Design/Theme.swift) carried
 * to the phone so the two read as one product.
 *
 * Neutral surfaces keep the conversation in focus. Colour is reserved for
 * agent state and actions, and each state owns one hue:
 *
 *   working      — cobalt, which is also the brand
 *   needs you    — tangerine, the one colour that asks for a tap
 *   idle         — no colour at all; nothing is happening, so nothing glows
 *
 * The greys are true neutrals, as on the Mac, and dark is a true black page
 * with near-black cards, so the phone and the Mac side by side are the same
 * paper and the same ink. Tokens the Mac has no need for (a camera scrim, the
 * undo bar's accent) are derived from its values.
 */
const light = {
  /** Page background. */
  paper: "#FAFAFA",
  /** Cards and sheets. */
  surface: "#FFFFFF",
  /** Tiles, code, secondary controls. */
  fill: "#EFEFEF",
  /** Borders on controls, pressed fills. */
  fillStrong: "#E4E4E4",
  /** Hairlines and card edges. */
  line: "#E5E5E5",
  /** A tool call's soft card, a step greyer than the page, as on the Mac;
   *  its edge; and the well its output sits in. */
  toolCard: "#F5F5F5",
  toolCardEdge: "#E8E8E8",
  toolWell: "#FAFAFA",

  text: "#101010",
  /** Body copy that should not compete with titles. */
  textSecondary: "#3C3C3C",
  /** Secondary text: paths, timestamps, tool summaries. */
  muted: "#737373",
  /** Tertiary: labels, placeholders, counts. */
  faint: "#999999",

  /** Primary buttons are ink pills; this is their fill and label. */
  inverse: "#101010",
  onInverse: "#FFFFFF",
  /** An accent that stays legible on the inverse fill (the undo bar). */
  inverseAccent: "#9DAAFF",

  /** An agent is working. Also the brand colour. */
  working: "#3268F5",
  workingText: "#1F35C7",
  workingWash: "#EEF1FF",
  workingSoft: "#D9DFFF",
  /** The working ring's second colour, which it flows into and back from. */
  loaderPink: "#F0407F",
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
  scrim: "rgba(16, 16, 16, 0.32)",
  /** Controls drawn over the camera, which is dark whatever the theme. */
  cameraScrim: "rgba(10, 10, 10, 0.62)",
  shadow: "#101010",
};

export type Palette = { [K in keyof typeof light]: string };

const dark: Palette = {
  paper: "#000000",
  surface: "#0D0D0D",
  fill: "#202020",
  fillStrong: "#303030",
  line: "#242424",
  toolCard: "#0D0D0D",
  toolCardEdge: "#202020",
  toolWell: "#101010",

  text: "#F2F2F2",
  textSecondary: "#D0D0D0",
  muted: "#A0A0A0",
  faint: "#737373",

  inverse: "#F2F2F2",
  onInverse: "#000000",
  inverseAccent: "#3268F5",

  working: "#578CFF",
  workingText: "#9DAAFF",
  workingWash: "#1A1F3D",
  workingSoft: "#28305E",
  loaderPink: "#FF5C9A",
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
  cameraScrim: "rgba(10, 10, 10, 0.62)",
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
    case "muse":
      return { name: "Muse", short: "mu" };
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
