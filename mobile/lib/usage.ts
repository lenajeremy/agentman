import type { Usage, UsageAgent, UsageBucket, UsageModel, UsagePart } from "./protocol";

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

/** The line under a model in the breakdown: how much of it Claude ran in fast
 *  mode, at a higher price, and how much went to advising another model, as
 *  on the Mac. Nothing for a model used only the usual way. */
export function modelNote(model: Pick<UsageModel, "fast" | "advisor" | "priced" | "credits">, metric: Metric): string | undefined {
  const value = (part: UsagePart) => metric === "cost" && model.priced
    ? money(part.cost)
    : tokens(part.tokens.input + part.tokens.cacheRead + part.tokens.cacheWrite + part.tokens.output);
  const notes = [
    model.fast ? `${value(model.fast)} in fast mode` : "",
    model.advisor ? `${value(model.advisor)} as advisor` : "",
    model.credits && model.credits > 0 ? credits(model.credits) : "",
  ].filter(Boolean);
  return notes.length > 0 ? notes.join(" · ") : undefined;
}

/** Credits, as Kiro counts them, to two places: "6.57 credits", as on the Mac. */
export function credits(value: number): string {
  const shown = value > 0 && value < 0.005 ? "<0.01" : value.toFixed(2);
  return `${shown} ${shown === "1.00" ? "credit" : "credits"}`;
}

/** An agent's credits, when it spent credits and nothing the totals count. */
export function creditsOnly(agent: Pick<UsageAgent, "credits" | "cost" | "tokens">): number | undefined {
  const t = agent.tokens;
  if (!agent.credits || agent.credits <= 0 || agent.cost !== 0 || t.input + t.cacheRead + t.cacheWrite + t.output !== 0) return undefined;
  return agent.credits;
}

export const CREDITS_NOTE = "Billed in credits, not in the totals";

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
    muse: ["#0866FF", "#5C9DFF"],
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

/** Whether a usage summary has any tokens or credits to display. */
export function hasUsageData(usage: Pick<Usage, "tokens" | "credits">): boolean {
  const count = usage.tokens.input + usage.tokens.cacheRead + usage.tokens.cacheWrite + usage.tokens.output;
  return count > 0 || (usage.credits ?? 0) > 0;
}

const UNIT_RANK: Record<Unit, number> = { hour: 0, day: 1, week: 2, month: 3 };

/**
 * Align a bucket's start timestamp to the nearest local calendar boundary so
 * computers in different time zones (for example a laptop in local time and a
 * UTC server) merge onto the same day/week/month instead of interleaving.
 */
export function alignBucketStart(startMs: number, unit: Unit): number {
  if (unit === "hour") return Math.round(startMs / 3_600_000) * 3_600_000;
  const raw = new Date(startMs);
  const floor = new Date(raw.getFullYear(), raw.getMonth(), raw.getDate());
  const ceil = new Date(raw.getFullYear(), raw.getMonth(), raw.getDate() + 1);
  const day = Math.abs(startMs - ceil.getTime()) < Math.abs(startMs - floor.getTime()) ? ceil : floor;
  if (unit === "day") return day.getTime();
  if (unit === "week") {
    const mondayOffset = (day.getDay() + 6) % 7;
    return new Date(day.getFullYear(), day.getMonth(), day.getDate() - mondayOffset).getTime();
  }
  return new Date(day.getFullYear(), day.getMonth(), 1).getTime();
}

function addTokens(a: Usage["tokens"], b: Usage["tokens"]): Usage["tokens"] {
  return {
    input: a.input + b.input,
    cacheRead: a.cacheRead + b.cacheRead,
    cacheWrite: a.cacheWrite + b.cacheWrite,
    output: a.output + b.output,
  };
}

function tokenSum(t: Usage["tokens"]): number {
  return t.input + t.cacheRead + t.cacheWrite + t.output;
}

function addPart(a?: UsagePart | null, b?: UsagePart | null): UsagePart | undefined {
  if (!a) return b ?? undefined;
  if (!b) return a;
  return { tokens: addTokens(a.tokens, b.tokens), cost: a.cost + b.cost };
}

function addMap(target: Record<string, number>, source?: Record<string, number>): void {
  if (!source) return;
  for (const [kind, value] of Object.entries(source)) {
    target[kind] = (target[kind] ?? 0) + value;
  }
}

/** Combine usage summaries from multiple paired computers into one summary. */
export function mergeUsage(items: readonly Usage[]): Usage {
  if (items.length === 1) return items[0];
  const zero = { input: 0, cacheRead: 0, cacheWrite: 0, output: 0 };
  if (items.length === 0) {
    return { days: 0, unit: "day", sessions: 0, tokens: { ...zero }, cost: { ...zero }, cacheSavings: 0, agents: [], buckets: [], models: [] };
  }

  let unit = unitOf(items[0]);
  for (const item of items) {
    const next = unitOf(item);
    if (UNIT_RANK[next] > UNIT_RANK[unit]) unit = next;
  }

  let sessionsCount = 0;
  let tokensTotal = { ...zero };
  let costTotal = { ...zero };
  let creditsTotal = 0;
  let cacheSavings = 0;
  let skipped = 0;

  const agentMap = new Map<string, UsageAgent>();
  const modelMap = new Map<string, UsageModel>();
  const bucketMap = new Map<number, UsageBucket>();

  for (const item of items) {
    sessionsCount += item.sessions;
    tokensTotal = addTokens(tokensTotal, item.tokens);
    costTotal = addTokens(costTotal, item.cost);
    creditsTotal += item.credits ?? 0;
    cacheSavings += item.cacheSavings;
    skipped += item.skipped ?? 0;

    for (const agent of item.agents ?? []) {
      const existing = agentMap.get(agent.kind);
      if (!existing) {
        agentMap.set(agent.kind, {
          kind: agent.kind,
          sessions: agent.sessions,
          tokens: { ...agent.tokens },
          cost: agent.cost,
          ...(agent.credits ? { credits: agent.credits } : {}),
        });
      } else {
        existing.sessions += agent.sessions;
        existing.tokens = addTokens(existing.tokens, agent.tokens);
        existing.cost += agent.cost;
        const nextCredits = (existing.credits ?? 0) + (agent.credits ?? 0);
        if (nextCredits > 0) existing.credits = nextCredits;
      }
    }

    for (const model of item.models ?? []) {
      const key = `${model.kind}\0${model.model}`;
      const existing = modelMap.get(key);
      if (!existing) {
        modelMap.set(key, {
          model: model.model,
          kind: model.kind,
          tokens: { ...model.tokens },
          cost: model.cost,
          priced: model.priced,
          ...(model.credits ? { credits: model.credits } : {}),
          ...(model.fast ? { fast: { tokens: { ...model.fast.tokens }, cost: model.fast.cost } } : {}),
          ...(model.advisor ? { advisor: { tokens: { ...model.advisor.tokens }, cost: model.advisor.cost } } : {}),
        });
      } else {
        existing.tokens = addTokens(existing.tokens, model.tokens);
        existing.cost += model.cost;
        existing.priced = existing.priced || model.priced;
        const nextCredits = (existing.credits ?? 0) + (model.credits ?? 0);
        if (nextCredits > 0) existing.credits = nextCredits;
        const fast = addPart(existing.fast, model.fast);
        if (fast) existing.fast = fast;
        const advisor = addPart(existing.advisor, model.advisor);
        if (advisor) existing.advisor = advisor;
      }
    }

    for (const bucket of item.buckets ?? []) {
      const start = alignBucketStart(bucket.start, unit);
      let existing = bucketMap.get(start);
      if (!existing) {
        existing = { start, cost: {}, tokens: {} };
        bucketMap.set(start, existing);
      }
      addMap(existing.cost!, bucket.cost);
      addMap(existing.tokens!, bucket.tokens);
      if (bucket.credits && Object.keys(bucket.credits).length > 0) {
        existing.credits ??= {};
        addMap(existing.credits, bucket.credits);
      }
    }
  }

  const agents = Array.from(agentMap.values()).sort(
    (a, b) => (b.cost - a.cost) || (tokenSum(b.tokens) - tokenSum(a.tokens)),
  );
  const models = Array.from(modelMap.values()).sort(
    (a, b) => (b.cost - a.cost) || (tokenSum(b.tokens) - tokenSum(a.tokens)),
  );
  const buckets = Array.from(bucketMap.values()).sort((a, b) => a.start - b.start);

  return {
    days: items[0].days,
    ...(unit === "hour" ? { hourly: true } : {}),
    unit,
    sessions: sessionsCount,
    tokens: tokensTotal,
    cost: costTotal,
    ...(creditsTotal > 0 ? { credits: creditsTotal } : {}),
    cacheSavings,
    agents,
    buckets,
    models,
    ...(skipped > 0 ? { skipped } : {}),
  };
}
