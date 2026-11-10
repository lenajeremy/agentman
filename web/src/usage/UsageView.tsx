// What the agents spent, and what is left of each plan (Views/UsageView.swift,
// Views/LimitsView.swift): two pages, Spending and Limits. The numbers and
// words are the phone's and the Mac's, from mobile/lib/usage.ts and
// limits.ts; the page is the Mac's.

import { useEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent } from "react";
import { ChartSpline, Gauge, Info, RotateCw } from "lucide-react";
import type { Limits, LimitWindow, AgentLimits, Usage, UsageAgent } from "@shared/protocol";
import {
  RANGES, axisLabel, axisMoney, bucketValue, credits, CREDITS_NOTE, creditsOnly, fraction, modelNote, momentLabel, money,
  monotonePath, niceAxis, percent, rangeTitle, rowLabel, sessions as sessionCount, tokens, unitAdjective, unitNoun, unitOf,
  type Metric, type Unit,
} from "@shared/usage";
import { checkedLabel, evenLeft, leftPercent, orderedWindows, pace, resetsLabel, type Pace } from "@shared/limits";
import { daemon } from "../daemon";
import { useStatus } from "../store";
import { agentName } from "../labels";
import { usePref } from "../prefs";
import { Loader } from "../shell/Loader";
import { AgentMark, ChromeButton } from "../ui";
import "../styles/usage.css";

const ESTIMATE = "What these tokens would cost through each provider's API. A subscription like Claude Max doesn't bill per token.";
const LIMITS_FOOTNOTE = "Read from Claude Code, Codex and Antigravity on this computer, as their own /usage and /status show. Other agents don't report plan limits.";
const LAST_TURN_HELP = "Codex's shared daemon isn't running, so these are the numbers Codex wrote down with its last reply.";
const PACE_HELP: Record<Pace, string> = {
  ahead: "Spending faster than the window passes",
  on: "Spending about as fast as the window passes",
  under: "Headroom left for the rest of the window",
};
const PACE_TITLE: Record<Pace, string> = { ahead: "Ahead of pace", on: "On pace", under: "Under pace" };

type Tab = "spend" | "limits";
type Breakdown = "model" | "day";

/** Each agent's colour, as Palette.agent: CSS that follows the appearance. */
const AGENT_COLORS: Record<string, [string, string]> = {
  claude: ["#D97757", "#E8896A"], codex: ["#1A1A1A", "#E4E4E4"], opencode: ["#3E8EBF", "#6BB3E0"], antigravity: ["#7C5CE6", "#A48BF5"],
  kiro: ["#9A4FE0", "#BE8BF2"], muse: ["#0866FF", "#5C9DFF"], cursor: ["#5B6170", "#B4B8C2"], "cursor-cli": ["#5B6170", "#B4B8C2"],
};
export function agentTint(kind: string): string {
  const [light, dark] = AGENT_COLORS[kind] ?? ["#8A8D94", "#9EA1A8"];
  return `light-dark(${light}, ${dark})`;
}

async function readUsage(days: number): Promise<Usage> {
  const reply = await daemon.request({ type: "usage", days }, 120_000);
  if (reply.type === "error") {
    const message = reply.error ?? "";
    throw new Error(message.includes("unsupported request") ? "Update am serve to see usage." : message.replace(/^daemon: /, ""));
  }
  if (!reply.usage) throw new Error("The daemon didn't say what the agents spent.");
  return reply.usage;
}

async function readLimits(force: boolean): Promise<Limits> {
  const reply = await daemon.request({ type: "limits", ...(force ? { force: true } : {}) }, 60_000);
  if (reply.type === "error") {
    const message = reply.error ?? "";
    throw new Error(message.includes("unsupported request") ? "Update am serve to see plan limits." : message.replace(/^daemon: /, ""));
  }
  if (!reply.limits) throw new Error("The daemon didn't say what is left of the plans.");
  return reply.limits;
}

// Each range read so far, shown at once when it is picked again while it is
// read afresh; kept while the app runs, as the Mac keeps them while the page is.
const read: Record<number, Usage> = {};

export function UsageView({ headerLeading }: { headerLeading: number }) {
  const [days, setDays] = usePref<number>("usageDays", 30);
  const [metric, setMetric] = usePref<Metric | "limits">("usageMetric", "cost");
  /** Cost or Tokens, as last picked, for going back from Limits. */
  const [spendMetric, setSpendMetric] = usePref<Metric>("usageSpendMetric", "cost");
  const [breakdown, setBreakdown] = usePref<Breakdown>("usageBreakdown", "model");
  const [summaries, setSummaries] = useState<Record<number, Usage>>(read);
  const [loading, setLoading] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [limits, setLimits] = useState<Limits | null>(null);
  const [readingLimits, setReadingLimits] = useState(false);
  const [limitsProblem, setLimitsProblem] = useState<string | null>(null);
  const tab: Tab = metric === "limits" ? "limits" : "spend";
  const scroller = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(1080);
  // Asked once am serve is there, and again after it comes back.
  const connected = useStatus().status === "connected";

  const load = async () => {
    const asked = days;
    setLoading(true);
    try {
      const usage = await readUsage(asked);
      read[asked] = usage;
      setSummaries({ ...read });
      setProblem(null);
    } catch (err) {
      if (!read[asked]) setProblem(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };
  const loadLimits = async (force: boolean) => {
    setReadingLimits(true);
    try {
      setLimits(await readLimits(force));
      setLimitsProblem(null);
    } catch (err) {
      if (!limits) setLimitsProblem(err instanceof Error ? err.message : String(err));
    } finally {
      setReadingLimits(false);
    }
  };

  useEffect(() => {
    if (connected) void load();
  }, [days, connected]);
  // Read when the Limits tab is opened, and only then (or on Read Again):
  // reading Claude's means starting Claude Code, which nobody should pay for
  // while looking at something else. A tab opened again within two minutes
  // gets the daemon's last read.
  useEffect(() => {
    if (tab === "limits" && connected) void loadLimits(false);
  }, [tab, connected]);

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const measure = () => setWidth(Math.min(el.clientWidth - 72, 1080));
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [tab, summaries[days] !== undefined, limits !== null]);

  const pickTab = (next: Tab) => {
    if (next === "limits") {
      if (metric !== "limits") setSpendMetric(metric);
      setMetric("limits");
    } else {
      setMetric(spendMetric);
    }
  };

  const busy = tab === "limits" ? readingLimits : loading;
  const usage = summaries[days];
  return (
    <div className="usage-page">
      <header className="usage-top drag" style={{ paddingLeft: 16 + headerLeading }}>
        <span className="usage-title">Usage</span>
        <span className="no-drag usage-tabs">
          <Segments options={["spend", "limits"] as Tab[]} value={tab} title={(t) => (t === "spend" ? "Spending" : "Limits")} onChange={pickTab} />
        </span>
        <span className="usage-spacer" />
        <span className="no-drag usage-top-controls">
          {tab === "spend" && (
            <>
              <Segments options={[...RANGES]} value={days} title={rangeTitle} onChange={setDays} />
              <Segments options={["cost", "tokens"] as Metric[]} value={metric as Metric} title={(m) => (m === "cost" ? "Cost" : "Tokens")} onChange={setMetric} />
            </>
          )}
          <span className={busy ? "usage-busy" : ""}>
            <ChromeButton icon={RotateCw} sf="arrow.clockwise" title={tab === "limits" ? "Read limits again" : "Read again"} onClick={() => {
              if (busy) return;
              void (tab === "limits" ? loadLimits(true) : load());
            }} />
          </span>
        </span>
      </header>
      <div className="usage-scroll" ref={scroller}>
        {tab === "limits" ? (
          limits ? (
            limits.agents.length === 0 ? <NoLimits /> : <div className="usage-column"><LimitsPage limits={limits} /></div>
          ) : limitsProblem ? (
            <Problem text={limitsProblem} retry={() => void loadLimits(true)} />
          ) : (
            <Reading text="Reading what is left of each plan…" />
          )
        ) : usage ? (
          totalTokens(usage) === 0 ? <NothingUsed days={days} /> : (
            <div className="usage-column">
              <SpendingPage usage={usage} metric={metric === "tokens" ? "tokens" : "cost"} breakdown={breakdown} setBreakdown={setBreakdown} width={width} />
            </div>
          )
        ) : problem ? (
          <Problem text={problem} retry={() => void load()} />
        ) : (
          <Reading text="Reading what the agents spent…" />
        )}
      </div>
    </div>
  );
}

const totalTokens = (u: { tokens: Usage["tokens"] }) => u.tokens.input + u.tokens.cacheRead + u.tokens.cacheWrite + u.tokens.output;

function Reading({ text }: { text: string }) {
  return <div className="usage-middle"><Loader size={16} /><span className="usage-caption">{text}</span></div>;
}

function Problem({ text, retry }: { text: string; retry: () => void }) {
  return (
    <div className="usage-middle">
      <span className="usage-problem">{text}</span>
      <button className="usage-link" onClick={retry}>Try Again</button>
    </div>
  );
}

/** Nothing used in the range. */
function NothingUsed({ days }: { days: number }) {
  return (
    <div className="usage-middle">
      <ChartSpline size={26} className="usage-faint" />
      <span className="usage-empty-title">{days === 0 ? "Nothing used yet" : days === 1 ? "Nothing used in the past day" : `Nothing used in the past ${days} days`}</span>
      <span className="usage-caption">Usage is read from Claude Code, Codex, OpenCode, Antigravity, Muse and Kiro's own records on this computer.</span>
    </div>
  );
}

/** A row of choices, the chosen one raised. */
function Segments<T extends string | number>({ options, value, title, onChange }: { options: T[]; value: T; title: (v: T) => string; onChange: (v: T) => void }) {
  return (
    <span className="segments">
      {options.map((o) => (
        <button key={String(o)} className={o === value ? "chosen" : ""} aria-pressed={o === value} onClick={() => onChange(o)}>{title(o)}</button>
      ))}
    </span>
  );
}

// MARK: - Spending

function SpendingPage({ usage, metric, breakdown, setBreakdown, width }: {
  usage: Usage; metric: Metric; breakdown: Breakdown; setBreakdown: (b: Breakdown) => void; width: number;
}) {
  const cost = metric === "cost";
  const unit = unitOf(usage);
  const agents = useMemo(() => {
    const all = usage.agents ?? [];
    return cost ? all : [...all].sort((a, b) => totalTokens(b) - totalTokens(a));
  }, [usage, cost]);
  const skipped = usage.skipped ?? 0;
  const side = width >= 760;
  return (
    <div className="spend">
      {skipped > 0 && (
        <div className="usage-caption">{skipped === 1 ? "1 record couldn't be read; usage may be short." : `${skipped.toLocaleString()} records couldn't be read; usage may be short.`}</div>
      )}
      <div className={side ? "spend-top side" : "spend-top"}>
        <Summary usage={usage} agents={agents} cost={cost} />
        <section className="spend-chart">
          <h3 className="usage-heading">{unitAdjective(unit) + (cost ? " cost" : " processed tokens")}</h3>
          <UsageChart usage={usage} metric={metric} agents={agents.map((a) => a.kind)} unit={unit} />
        </section>
      </div>
      <Totals usage={usage} />
      <ByKind usage={usage} cost={cost} width={width} />
      <BreakdownTable usage={usage} metric={metric} breakdown={breakdown} setBreakdown={setBreakdown} unit={unit} />
    </div>
  );
}

/** The total and each agent's share. */
function Summary({ usage, agents, cost }: { usage: Usage; agents: UsageAgent[]; cost: boolean }) {
  return (
    <section className="spend-summary">
      <div>
        <div className="spend-total">{cost ? money(usage.cost.input + usage.cost.cacheRead + usage.cost.cacheWrite + usage.cost.output) : tokens(totalTokens(usage))}</div>
        <div className="usage-caption spend-sessions">
          {sessionCount(usage.sessions) + (cost ? " · API estimate" : "")}
          {cost && <span title={ESTIMATE}><Info size={11} /></span>}
        </div>
      </div>
      <div className="spend-agents">
        {agents.map((agent) => {
          const totalCost = usage.cost.input + usage.cost.cacheRead + usage.cost.cacheWrite + usage.cost.output;
          const share = cost ? fraction(agent.cost, totalCost) : fraction(totalTokens(agent), totalTokens(usage));
          const other = cost ? `${tokens(totalTokens(agent))} tokens` : money(agent.cost);
          // Kiro counts credits, not tokens: its row says those instead.
          const onlyCredits = creditsOnly(agent);
          return (
            <div key={agent.kind} className="spend-agent">
              <div className="spend-agent-line">
                <span className="dot" style={{ background: agentTint(agent.kind) }} />
                <AgentMark kind={agent.kind} size={15} />
                <span className="spend-agent-name">{agentName(agent.kind)}</span>
                <span className="spend-agent-sessions">{sessionCount(agent.sessions)}</span>
                <span className="usage-spacer" />
                <span className="spend-agent-value">{onlyCredits !== undefined ? credits(onlyCredits) : cost ? money(agent.cost) : tokens(totalTokens(agent))}</span>
              </div>
              <div className="spend-agent-share">{onlyCredits !== undefined ? CREDITS_NOTE : `${percent(share)}${cost ? " of cost · " : " of tokens · "}${other}`}</div>
            </div>
          );
        })}
      </div>
    </section>
  );
}

/** Each agent's day (or hour) as a line over its own soft area, not stacked,
 *  with what each spent where the pointer is. */
function UsageChart({ usage, metric, agents, unit }: { usage: Usage; metric: Metric; agents: string[]; unit: Unit }) {
  const buckets = usage.buckets ?? [];
  const box = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 600, height: 250 });
  const [hovered, setHovered] = useState<number | null>(null);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const measure = () => setSize({ width: el.clientWidth, height: el.clientHeight });
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  const top = Math.max(0, ...buckets.flatMap((b) => agents.map((k) => bucketValue(b, k, metric))));
  const axis = niceAxis(top, metric);
  // Room for the y axis's labels, and the x axis's under the plot.
  const left = 52;
  const bottom = 22;
  const plotWidth = Math.max(10, size.width - left);
  const plotHeight = Math.max(10, size.height - bottom);
  const x = (i: number) => left + (buckets.length <= 1 ? plotWidth / 2 : (i / (buckets.length - 1)) * plotWidth);
  const y = (v: number) => plotHeight - (v / axis.top) * plotHeight;
  const ticks: number[] = [];
  for (let v = 0; v <= axis.top + 1e-9; v += axis.step) ticks.push(v);
  const spanMs = buckets.length ? buckets[buckets.length - 1].start - buckets[0].start : 0;
  const marks = buckets.length ? [0, Math.floor(buckets.length / 2), buckets.length - 1] : [];
  const format = (v: number) => (metric === "cost" ? money(v) : tokens(v));

  const point = (e: ReactMouseEvent<SVGSVGElement>) => {
    if (!buckets.length) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const at = e.clientX - rect.left - left;
    const i = buckets.length <= 1 ? 0 : Math.round((at / plotWidth) * (buckets.length - 1));
    setHovered(Math.max(0, Math.min(buckets.length - 1, i)));
  };

  const shown = hovered !== null ? buckets[hovered] : null;
  const tipLeft = hovered !== null ? Math.min(x(hovered) + 10, size.width - 196) : 0;
  return (
    <div className="usage-chart" ref={box}>
      <svg width={size.width} height={size.height} onMouseMove={point} onMouseLeave={() => setHovered(null)}>
        {ticks.map((v) => (
          <g key={v}>
            <line x1={left} x2={size.width} y1={y(v)} y2={y(v)} className="chart-grid" />
            <text x={left - 8} y={y(v) + 3.5} className="chart-label" textAnchor="end">{metric === "cost" ? axisMoney(v, axis.step) : tokens(v)}</text>
          </g>
        ))}
        {agents.map((kind) => {
          const pts = buckets.map((b, i) => ({ x: x(i), y: y(bucketValue(b, kind, metric)) }));
          if (!pts.length) return null;
          const line = monotonePath(pts);
          const area = `${line} L ${pts[pts.length - 1].x} ${plotHeight} L ${pts[0].x} ${plotHeight} Z`;
          return (
            <g key={kind} style={{ color: agentTint(kind) }}>
              <path d={area} className="chart-area" />
              <path d={line} className="chart-line" />
            </g>
          );
        })}
        {marks.map((i, n) => (
          <text key={n} x={x(i)} y={size.height - 4} className="chart-label" textAnchor={n === 0 ? "start" : n === 2 ? "end" : "middle"}>
            {axisLabel(unit, new Date(buckets[i].start), spanMs)}
          </text>
        ))}
        {hovered !== null && <line x1={x(hovered)} x2={x(hovered)} y1={0} y2={plotHeight} className="chart-rule" />}
      </svg>
      {shown && (
        <div className="chart-tip" style={{ left: tipLeft }}>
          <div className="chart-tip-when">{momentLabel(unit, new Date(shown.start))}</div>
          {agents.map((kind) => (
            <div key={kind} className="chart-tip-row">
              <AgentMark kind={kind} size={12} />
              <span className="chart-tip-name">{agentName(kind)}</span>
              <span className="usage-spacer" />
              <span className="chart-tip-value">{format(bucketValue(shown, kind, metric))}</span>
            </div>
          ))}
          <div className="chart-tip-rule" />
          <div className="chart-tip-row">
            <span className="chart-tip-name">Total</span>
            <span className="usage-spacer" />
            <span className="chart-tip-value">{format(agents.reduce((n, k) => n + bucketValue(shown, k, metric), 0))}</span>
          </div>
        </div>
      )}
    </div>
  );
}

function Totals({ usage }: { usage: Usage }) {
  const stat = (title: string, value: string) => (
    <div className="stat"><div className="stat-title">{title}</div><div className="stat-value">{value}</div></div>
  );
  return (
    <section>
      <h3 className="usage-heading">Totals</h3>
      <div className="stats">
        {stat("Processed tokens", tokens(totalTokens(usage)))}
        {stat("Cached input", tokens(usage.tokens.cacheRead))}
        {stat("Uncached input", tokens(usage.tokens.input))}
        {stat("Output", tokens(usage.tokens.output))}
        {stat("Cache savings", money(usage.cacheSavings))}
      </div>
    </section>
  );
}

/** Input, cache read, cache write and output, told apart by hue. */
function ByKind({ usage, cost, width }: { usage: Usage; cost: boolean; width: number }) {
  const t = usage.tokens;
  const c = usage.cost;
  const kinds = [
    { name: "Input", color: "var(--token-input)", value: cost ? c.input : t.input },
    { name: "Cache read", color: "var(--token-cache-read)", value: cost ? c.cacheRead : t.cacheRead },
    { name: "Cache write", color: "var(--token-cache-write)", value: cost ? c.cacheWrite : t.cacheWrite },
    { name: "Output", color: "var(--token-output)", value: cost ? c.output : t.output },
  ];
  const total = kinds.reduce((n, k) => n + k.value, 0);
  return (
    <section>
      <h3 className="usage-heading">{cost ? "Cost by type" : "Tokens by type"}</h3>
      <div className="kinds-bar" style={{ width: Math.min(Math.max(width * 0.5, 280), 560) }}>
        {kinds.filter((k) => k.value > 0).map((k) => (
          <span key={k.name} style={{ background: k.color, flexGrow: k.value / Math.max(total, 1e-9), minWidth: 6 }} />
        ))}
      </div>
      <div className="kinds-legend">
        {kinds.map((k) => (
          <span key={k.name} className="kinds-item">
            <span className="kinds-swatch" style={{ background: k.color }} />
            <span className="kinds-name">{k.name}</span>
            <span className="kinds-value">{cost ? money(k.value) : tokens(k.value)}</span>
          </span>
        ))}
      </div>
    </section>
  );
}

interface Row {
  id: string;
  name: string;
  kind?: string;
  cost: number | null;
  tokens: number;
  note?: string;
  parts: { kind: string; value: number }[];
}

/** Each model's part, or each day's. */
function BreakdownTable({ usage, metric, breakdown, setBreakdown, unit }: {
  usage: Usage; metric: Metric; breakdown: Breakdown; setBreakdown: (b: Breakdown) => void; unit: Unit;
}) {
  const cost = metric === "cost";
  const totalCost = usage.cost.input + usage.cost.cacheRead + usage.cost.cacheWrite + usage.cost.output;
  const rows: Row[] = breakdown === "model"
    ? [...(usage.models ?? [])]
      .sort((a, b) => (cost ? b.cost - a.cost || totalTokens(b) - totalTokens(a) : totalTokens(b) - totalTokens(a)))
      .map((m) => ({ id: `${m.kind}/${m.model}`, name: m.model || "Unknown model", kind: m.kind, cost: m.priced ? m.cost : null, tokens: totalTokens(m), note: modelNote(m, metric), parts: [] }))
    : [...(usage.buckets ?? [])].reverse().flatMap((b) => {
      const kinds = (usage.agents ?? []).map((a) => a.kind);
      const sum = Object.values(b.tokens ?? {}).reduce((n, v) => n + v, 0);
      if (sum <= 0) return [];
      return [{
        id: String(b.start), name: rowLabel(unit, new Date(b.start)), cost: Object.values(b.cost ?? {}).reduce((n, v) => n + v, 0), tokens: sum,
        parts: kinds.map((k) => ({ kind: k, value: bucketValue(b, k, metric) })),
      }];
    });
  const share = (r: Row) => (cost ? fraction(r.cost ?? 0, totalCost) : fraction(r.tokens, totalTokens(usage)));
  const most = Math.max(0, ...rows.map(share));
  const noun = breakdown === "model" ? "Model" : unitNoun(unit);
  return (
    <section>
      <div className="breakdown-head">
        <h3 className="usage-heading">Breakdown</h3>
        <span className="usage-spacer" />
        <Segments options={["model", "day"] as Breakdown[]} value={breakdown} title={(b) => (b === "model" ? "Model" : unitNoun(unit))} onChange={setBreakdown} />
      </div>
      <div className="breakdown">
        <div className="breakdown-row head">
          <span className="b-rank">#</span>
          <span className="b-name">{noun}</span>
          <span className="b-cost">Cost</span>
          <span className="b-share">Share</span>
          <span className="b-tokens">Tokens</span>
        </div>
        {rows.map((r, i) => {
          const s = share(r);
          const length = Math.max((240 * s) / Math.max(most, 1e-9), 4);
          const partsTotal = r.parts.reduce((n, p) => n + p.value, 0);
          return (
            <div key={r.id} className="breakdown-row">
              <span className="b-rank">{i + 1}</span>
              <span className="b-name">
                <span className="b-title">{r.kind && <AgentMark kind={r.kind} size={14} />}<span className="b-label">{r.name}</span></span>
                <span className="b-bar" style={{ width: length }}>
                  {r.kind ? <span style={{ background: agentTint(r.kind), flexGrow: 1 }} />
                    : r.parts.filter((p) => p.value > 0).map((p) => <span key={p.kind} style={{ background: agentTint(p.kind), flexGrow: p.value / Math.max(partsTotal, 1e-9) }} />)}
                </span>
                {r.note && <span className="b-note">{r.note}</span>}
              </span>
              <span className="b-cost" title={r.cost === null ? "No known price for this model" : undefined}>{r.cost === null ? "—" : money(r.cost)}</span>
              <span className="b-share">{cost && r.cost === null ? "—" : percent(s)}</span>
              <span className="b-tokens">{tokens(r.tokens)}</span>
            </div>
          );
        })}
      </div>
    </section>
  );
}

// MARK: - Limits

/** What is left of each agent's plan, as T3 Code's Limits tab shows it. The
 *  numbers are each agent's own: Claude Code's /usage, Codex's /status,
 *  Antigravity's /usage. */
function LimitsPage({ limits }: { limits: Limits }) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 30_000);
    return () => window.clearInterval(timer);
  }, []);
  return (
    <div className="limits">
      {limits.agents.map((agent) => <LimitsSection key={agent.kind} agent={agent} now={now} />)}
      <div className="usage-caption faint">{LIMITS_FOOTNOTE}</div>
    </div>
  );
}

function NoLimits() {
  return (
    <div className="usage-middle">
      <Gauge size={26} className="usage-faint" />
      <span className="usage-empty-title">No plan limits to show</span>
      <span className="usage-caption">{LIMITS_FOOTNOTE}</span>
    </div>
  );
}

function LimitsSection({ agent, now }: { agent: AgentLimits; now: number }) {
  return (
    <section className="limits-section">
      <div className="limits-head">
        <AgentMark kind={agent.kind} size={16} />
        <span className="usage-heading">{agentName(agent.kind)}</span>
        {agent.plan && <span className="limits-plan">{agent.plan}</span>}
        <span className="usage-spacer" />
        <span className="usage-caption" title={agent.lastTurn ? LAST_TURN_HELP : undefined}>{checkedLabel(agent, now)}</span>
      </div>
      {agent.problem && <div className="usage-caption">{agent.problem}</div>}
      <div className="limits-cards">
        {orderedWindows(agent.windows ?? []).map((w) => <LimitCard key={w.id} window={w} color={agentTint(agent.kind)} now={now} />)}
      </div>
    </section>
  );
}

/** One window: what is left, big, beside a bar of it. */
function LimitCard({ window: w, color, now }: { window: LimitWindow; color: string; now: number }) {
  const left = leftPercent(w);
  const even = evenLeft(w, now);
  const p = pace(w, now);
  const resets = resetsLabel(w, now);
  return (
    <div className="limit-card">
      <div className="limit-left">
        <span className="limit-label" title={w.label}>{w.label}</span>
        <span className="limit-amount"><span className={left === 0 ? "limit-number spent" : "limit-number"}>{left}%</span><span className="limit-word">left</span></span>
      </div>
      <div className="limit-right">
        <div className="limit-bar">
          <span className="limit-fill" style={{ width: `${left}%`, background: `color-mix(in srgb, ${color} 42%, transparent)` }} />
          {even !== null && (
            <span className="limit-even" style={{ left: `calc(${even}% - 1px)` }} title={`Where spending evenly through the window would leave it: ${even}% left`} />
          )}
        </div>
        <div className="limit-line">
          {resets ? <><RotateCw size={10} strokeWidth={2.4} /><span>{resets}</span></> : <span>{left === 100 ? "Not used yet" : " "}</span>}
          <span className="usage-spacer" />
          {p && <span className={p === "ahead" ? "limit-pace ahead" : "limit-pace"} title={PACE_HELP[p]}>{PACE_TITLE[p]}</span>}
        </div>
      </div>
    </div>
  );
}
