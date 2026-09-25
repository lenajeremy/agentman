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

  scrim: "rgba(0, 0, 0, 0.5)",
  cameraScrim: "rgba(10, 11, 13, 0.62)",
  shadow: "#000000",
};

export const palettes: Record<"light" | "dark", Palette> = { light, dark };

/**
 * Geist for everything a person wrote or reads as interface; Geist Mono for
 * machine-authored text. Paths, session ids, shell commands and agent names
 * are strings a program produced, and setting them in mono says so at a
 * glance.
 */
export const font = {
  sans: "Geist_400Regular",
  sansMedium: "Geist_500Medium",
  sansBold: "Geist_600SemiBold",
  mono: "GeistMono_400Regular",
  monoMedium: "GeistMono_500Medium",
} as const;

export const size = {
  /** Section labels, counts. */
  label: 12,
  caption: 13,
  body: 15,
  title: 17,
  heading: 22,
  display: 34,
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
