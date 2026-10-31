import type { Usage, UsageBucket } from "./protocol";

/**
 * The usage screen's arithmetic and words, kept apart from the screen so they
 * can be tested: what the Mac app shows, in the same numbers and labels.
 */

/** The ranges, in days; 0 is all of it. */
export const RANGES = [1, 7, 30, 90, 180, 365, 0] as const;

export function rangeTitle(days: number): string {
  switch (days) {
    case 0: return "All";
    case 1: return "24h";
    case 180: return "6m";
    case 365: return "1y";
    default: return `${days}d`;
  }
}

export type Metric = "cost" | "tokens";
export type Unit = "hour" | "day" | "week" | "month";

/** How wide a summary's buckets are; a Mac that predates units says hourly. */
export function unitOf(usage: Pick<Usage, "unit" | "hourly">): Unit {
  if (usage.unit === "hour" || usage.unit === "day" || usage.unit === "week" || usage.unit === "month") return usage.unit;
  return usage.hourly ? "hour" : "day";
}

export function unitAdjective(unit: Unit): string {
  return { hour: "Hourly", day: "Daily", week: "Weekly", month: "Monthly" }[unit];
}

export function unitNoun(unit: Unit): string {
  return { hour: "Hour", day: "Day", week: "Week", month: "Month" }[unit];
}

/** The chart's axis: hours, days, or months with the year once the chart
 *  spans most of one. */
export function axisLabel(unit: Unit, date: Date, spanMs: number, locale?: string): string {
  if (unit === "hour") return date.toLocaleTimeString(locale, { hour: "numeric" });
  if (unit === "month" || spanMs > 300 * 86_400_000) {
    return date.toLocaleDateString(locale, { month: "short", year: "numeric" }).toUpperCase();
  }
  return date.toLocaleDateString(locale, { month: "short", day: "numeric" }).toUpperCase();
}

/** A bucket the finger is on. */
export function momentLabel(unit: Unit, date: Date, locale?: string): string {
  switch (unit) {
    case "hour": return date.toLocaleString(locale, { weekday: "short", hour: "numeric" });
    case "day": return date.toLocaleDateString(locale, { weekday: "short", month: "short", day: "numeric" });
    case "week": return `Week of ${date.toLocaleDateString(locale, { month: "short", day: "numeric" })}`;
    case "month": return date.toLocaleDateString(locale, { month: "long", year: "numeric" });
  }
}

/** A bucket in the breakdown. */
export function rowLabel(unit: Unit, date: Date, locale?: string): string {
  if (unit === "week") {
    return `Week of ${date.toLocaleDateString(locale, { day: "numeric", month: "short", year: "numeric" })}`;
  }
  return momentLabel(unit, date, locale);
}

const dollars = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: 2, maximumFractionDigits: 2 });
const wholeDollars = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: 0, maximumFractionDigits: 0 });

/** Dollars and cents; a little under a cent is "<$0.01". */
export function money(value: number): string {
  if (value > 0 && value < 0.005) return "<$0.01";
  return dollars.format(value);
}

/** A chart's axis: whole dollars, unless its steps aren't. */
export function axisMoney(value: number, step: number): string {
  return Math.round(step) === step ? wholeDollars.format(value) : dollars.format(value);
}

/** Three figures, in thousands, millions or billions: 12.6K, 410M, 2.30B. */
export function tokens(count: number): string {
  for (const [size, unit] of [[1e9, "B"], [1e6, "M"], [1e3, "K"]] as const) {
    if (Math.abs(count) >= size * 0.9995) {
      const scaled = count / size;
      return scaled.toFixed(scaled >= 99.95 ? 0 : scaled >= 9.995 ? 1 : 2) + unit;
    }
  }
  return String(Math.round(count));
}

/** A share, to a tenth of a percent; under that, "<0.1%". */
export function percent(share: number): string {
  if (share <= 0) return "0%";
  if (share < 0.0005) return "<0.1%";
  return `${(share * 100).toFixed(1)}%`;
}

export function sessions(count: number): string {
  return count === 1 ? "1 session" : `${count.toLocaleString("en-US")} sessions`;
}

export function fraction(part: number, whole: number): number {
  return whole > 0 ? part / whole : 0;
}

/** An agent's colour in the chart and beside its name, as on the Mac. */
export function agentColor(kind: string, dark: boolean): string {
  const colors: Record<string, [string, string]> = {
    claude: ["#D97757", "#E8896A"],
    codex: ["#1A1A1A", "#E4E4E4"],
    opencode: ["#3E8EBF", "#6BB3E0"],
    antigravity: ["#7C5CE6", "#A48BF5"],
    kiro: ["#9A4FE0", "#BE8BF2"],
    cursor: ["#5B6170", "#B4B8C2"],
    "cursor-cli": ["#5B6170", "#B4B8C2"],
  };
  const [light, night] = colors[kind] ?? ["#8A8D94", "#9EA1A8"];
  return dark ? night : light;
}

/** The kinds of tokens, output the strongest, as on the Mac. */
export function kindColors(dark: boolean): { input: string; cacheRead: string; cacheWrite: string; output: string } {
  return dark
    ? { input: "#8E8A8D", cacheRead: "#4F4C4E", cacheWrite: "#B4B0B3", output: "#ECE8EB" }
    : { input: "#7D787C", cacheRead: "#B9B5B8", cacheWrite: "#585256", output: "#1E1A1D" };
}

/** One agent's value in a bucket. */
export function bucketValue(bucket: UsageBucket, kind: string, metric: Metric): number {
  return (metric === "cost" ? bucket.cost?.[kind] : bucket.tokens?.[kind]) ?? 0;
}

/** The chart's axis: a round step, three or four of them up to past the top. */
export function niceAxis(top: number, metric: Metric): { top: number; step: number } {
  const highest = Math.max(top, metric === "cost" ? 0.01 : 10);
  const rough = highest / 3;
  const magnitude = 10 ** Math.floor(Math.log10(rough));
  const step = [1, 2, 2.5, 5, 10].map((n) => n * magnitude).find((n) => n >= rough) ?? rough;
  return { top: Math.ceil(highest / step - 1e-9) * step, step };
}

/**
 * A smooth line through the points that never overshoots them (monotone
 * cubic, Fritsch–Carlson), as SVG path data: a chart of spend never dips
 * below zero between two quiet days.
 */
export function monotonePath(points: { x: number; y: number }[]): string {
  const n = points.length;
  if (n === 0) return "";
  if (n === 1) return `M${points[0].x},${points[0].y}`;
  const dx: number[] = [];
  const slope: number[] = [];
  for (let i = 0; i < n - 1; i++) {
    dx.push(points[i + 1].x - points[i].x);
    slope.push(dx[i] === 0 ? 0 : (points[i + 1].y - points[i].y) / dx[i]);
  }
  const tangent: number[] = [slope[0]];
  for (let i = 1; i < n - 1; i++) {
    tangent.push(slope[i - 1] * slope[i] <= 0 ? 0 : (slope[i - 1] + slope[i]) / 2);
  }
  tangent.push(slope[n - 2]);
  for (let i = 0; i < n - 1; i++) {
    if (slope[i] === 0) {
      tangent[i] = 0;
      tangent[i + 1] = 0;
      continue;
    }
    const a = tangent[i] / slope[i];
    const b = tangent[i + 1] / slope[i];
    const h = a * a + b * b;
    if (h > 9) {
      const t = 3 / Math.sqrt(h);
      tangent[i] = t * a * slope[i];
      tangent[i + 1] = t * b * slope[i];
    }
  }
  let d = `M${round(points[0].x)},${round(points[0].y)}`;
  for (let i = 0; i < n - 1; i++) {
    const p = points[i];
    const q = points[i + 1];
    const third = dx[i] / 3;
    d += `C${round(p.x + third)},${round(p.y + tangent[i] * third)},${round(q.x - third)},${round(q.y - tangent[i + 1] * third)},${round(q.x)},${round(q.y)}`;
  }
  return d;
}

function round(value: number): number {
  return Math.round(value * 10) / 10;
}
