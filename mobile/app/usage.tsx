import Feather from "@expo/vector-icons/Feather";
import { useLocalSearchParams } from "expo-router";
import { useCallback, useEffect, useRef, useState } from "react";
import { StyleSheet, Text, View, type LayoutChangeEvent } from "react-native";
import Svg, { G, Line, Path, Text as SvgText } from "react-native-svg";

import { AgentIcon } from "../components/AgentIcon";
import { Loader } from "../components/Loader";
import { MotionPressable } from "../components/MotionPressable";
import { Section, Segmented, SettingsPage } from "../components/SettingsKit";
import { useStyles, useTheme } from "../lib/appearance";
import type { Usage, UsageAgent } from "../lib/protocol";
import { useStore } from "../lib/store";
import { agentLabel, font, Palette, radius, scaled, size, space } from "../lib/theme";
import {
  agentColor, axisLabel, axisMoney, bucketValue, fraction, kindColors, momentLabel, money, monotonePath,
  niceAxis, percent, RANGES, rangeTitle, rowLabel, sessions, tokens, unitAdjective, unitNoun, unitOf,
  type Metric,
} from "../lib/usage";

/**
 * What the agents on the Mac spent, as its usage page shows it: the total,
 * each agent's share, a chart of each day (each hour, week or month), the
 * tokens by kind, and each model's or day's part. Costs are what the API
 * would charge: an estimate, as a subscription doesn't bill per token.
 */

// The range and measure last looked at, kept while the app runs.
let lastDays = 30;
let lastMetric: Metric = "cost";

export default function UsageScreen() {
  const store = useStore();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  // A link can name the range and measure: /usage?days=90&metric=tokens.
  const params = useLocalSearchParams<{ days?: string; metric?: string }>();
  const linked = Number(params.days);
  const [days, setDays] = useState((RANGES as readonly number[]).includes(linked) && params.days !== undefined ? linked : lastDays);
  const [metric, setMetric] = useState<Metric>(params.metric === "tokens" || params.metric === "cost" ? params.metric : lastMetric);
  const [read, setRead] = useState<Record<number, Usage>>({});
  const [problem, setProblem] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const asked = useRef(0);

  const load = useCallback(async (range: number) => {
    const ask = ++asked.current;
    setLoading(true);
    try {
      const usage = await store.usage(range);
      setRead((current) => ({ ...current, [range]: usage }));
      if (ask === asked.current) setProblem(null);
    } catch (error) {
      if (ask === asked.current) setProblem(error instanceof Error ? error.message : String(error));
    } finally {
      if (ask === asked.current) setLoading(false);
    }
  }, [store]);

  useEffect(() => {
    lastDays = days;
    void load(days);
  }, [days, load]);
  useEffect(() => { lastMetric = metric; }, [metric]);

  const usage = read[days];
  return (
    <SettingsPage
      title="Usage"
      action={
        <MotionPressable onPress={() => void load(days)} disabled={loading} hitSlop={12} pressedScale={0.92}
          style={styles.iconButton} accessibilityRole="button" accessibilityLabel="Read again">
          {loading && usage ? <Loader size={9} /> : <Feather name="rotate-cw" size={17} color={color.text} />}
        </MotionPressable>
      }
    >
      <View style={styles.controls}>
        <Segmented options={RANGES.map((range) => ({ value: String(range), label: rangeTitle(range) }))}
          value={String(days)} onChange={(value) => setDays(Number(value))} />
        <View style={styles.metric}>
          <Segmented options={[{ value: "cost", label: "Cost" }, { value: "tokens", label: "Tokens" }]}
            value={metric} onChange={(value) => setMetric(value as Metric)} />
        </View>
      </View>
      {usage ? (
        usage.tokens.input + usage.tokens.cacheRead + usage.tokens.cacheWrite + usage.tokens.output === 0
          ? <Text style={styles.note}>{days === 0 ? "Nothing used yet." : `Nothing used in the past ${days === 1 ? "day" : `${days} days`}.`}</Text>
          : <UsagePage usage={usage} metric={metric} />
      ) : problem ? (
        <View style={styles.problem}>
          <Text style={styles.note}>{problem}</Text>
          <MotionPressable onPress={() => void load(days)} accessibilityRole="button" hitSlop={8}>
            <Text style={styles.link}>Try again</Text>
          </MotionPressable>
        </View>
      ) : (
        <View style={styles.loading}>
          <Loader size={14} />
          <Text style={styles.note}>Reading what the agents spent…</Text>
        </View>
      )}
    </SettingsPage>
  );
}

/** The page for one range, also drawn on its own for pictures of it. */
export function UsagePage({ usage, metric, pointing }: { usage: Usage; metric: Metric; pointing?: number }) {
  const styles = useStyles(makeStyles);
  const { color, scheme } = useTheme();
  const dark = scheme === "dark";
  const cost = metric === "cost";
  const unit = unitOf(usage);
  const [breakdown, setBreakdown] = useState<"model" | "bucket">("model");
  const total = (t: Usage["tokens"]) => t.input + t.cacheRead + t.cacheWrite + t.output;
  const agents = [...(usage.agents ?? [])].sort((a, b) => cost ? b.cost - a.cost : total(b.tokens) - total(a.tokens));

  return (
    <View>
      {usage.skipped ? (
        <Text style={[styles.note, styles.skipped]}>
          {usage.skipped === 1 ? "1 record couldn't be read" : `${usage.skipped.toLocaleString("en-US")} records couldn't be read`}; usage may be short.
        </Text>
      ) : null}

      <View style={styles.summary}>
        <Text style={styles.total} numberOfLines={1} adjustsFontSizeToFit>
          {cost ? money(total(usage.cost)) : tokens(total(usage.tokens))}
        </Text>
        <Text style={styles.subtle}>{sessions(usage.sessions)}{cost ? " · API estimate" : ""}</Text>
      </View>

      <Section>
        {agents.map((agent, index) => (
          <View key={agent.kind}>
            {index > 0 ? <View style={styles.divider} /> : null}
            <AgentRow agent={agent} usage={usage} metric={metric} dark={dark} />
          </View>
        ))}
      </Section>

      <Section label={`${unitAdjective(unit)} ${cost ? "cost" : "processed tokens"}`}>
        <Chart usage={usage} metric={metric} kinds={agents.map((agent) => agent.kind)} dark={dark} pointing={pointing} />
      </Section>

      <Section label="Totals">
        <View style={styles.stats}>
          <Stat label="Processed tokens" value={tokens(total(usage.tokens))} />
          <Stat label="Cached input" value={tokens(usage.tokens.cacheRead)} />
          <Stat label="Uncached input" value={tokens(usage.tokens.input)} />
          <Stat label="Output" value={tokens(usage.tokens.output)} />
          <Stat label="Cache savings" value={money(usage.cacheSavings)} />
        </View>
      </Section>

      <Section label={cost ? "Cost by type" : "Tokens by type"}>
        <ByKind usage={usage} metric={metric} dark={dark} />
      </Section>

      <View style={styles.breakdownHead}>
        <Text style={styles.sectionTitle}>Breakdown</Text>
        <View style={styles.breakdownSwitch}>
          <Segmented options={[{ value: "model", label: "Model" }, { value: "bucket", label: unitNoun(unit) }]}
            value={breakdown} onChange={(value) => setBreakdown(value as "model" | "bucket")} />
        </View>
      </View>
      <View style={styles.card}>
        <Breakdown usage={usage} metric={metric} by={breakdown} dark={dark} />
      </View>
    </View>
  );
}

function AgentRow({ agent, usage, metric, dark }: { agent: UsageAgent; usage: Usage; metric: Metric; dark: boolean }) {
  const styles = useStyles(makeStyles);
  const cost = metric === "cost";
  const all = (t: Usage["tokens"]) => t.input + t.cacheRead + t.cacheWrite + t.output;
  const share = cost ? fraction(agent.cost, all(usage.cost)) : fraction(all(agent.tokens), all(usage.tokens));
  return (
    <View style={styles.agentRow}>
      <View style={styles.agentLine}>
        <View style={[styles.dot, { backgroundColor: agentColor(agent.kind, dark) }]} />
        <AgentIcon kind={agent.kind} size={16} />
        <Text style={styles.agentName} numberOfLines={1}>{agentLabel(agent.kind).name}</Text>
        <Text style={styles.agentSessions} numberOfLines={1}>{sessions(agent.sessions)}</Text>
        <Text style={styles.agentValue}>{cost ? money(agent.cost) : tokens(all(agent.tokens))}</Text>
      </View>
      <Text style={styles.agentShare}>
        {percent(share)} of {cost ? "cost" : "tokens"} · {cost ? `${tokens(all(agent.tokens))} tokens` : money(agent.cost)}
      </Text>
    </View>
  );
}

const CHART_HEIGHT = 190;
const CHART_LEFT = 46;
const CHART_BOTTOM = 22;
const CHART_TOP = 8;
const CHART_RIGHT = 8;

/** Each agent's line over its own soft area, not stacked; a finger on the
 *  chart reads out that bucket. */
function Chart({ usage, metric, kinds, dark, pointing }: {
  usage: Usage; metric: Metric; kinds: string[]; dark: boolean; pointing?: number;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [width, setWidth] = useState(0);
  const [selected, setSelected] = useState<number | null>(pointing ?? null);
  const buckets = usage.buckets ?? [];
  const unit = unitOf(usage);
  const cost = metric === "cost";
  const top = Math.max(0, ...buckets.flatMap((bucket) => kinds.map((kind) => bucketValue(bucket, kind, metric))));
  const axis = niceAxis(top, metric);
  const plotWidth = Math.max(0, width - CHART_LEFT - CHART_RIGHT);
  const plotHeight = CHART_HEIGHT - CHART_TOP - CHART_BOTTOM;
  const x = (index: number) => CHART_LEFT + (buckets.length <= 1 ? plotWidth / 2 : (index * plotWidth) / (buckets.length - 1));
  const y = (value: number) => CHART_TOP + plotHeight - (value / axis.top) * plotHeight;
  const format = (value: number) => (cost ? money(value) : tokens(value));
  const span = buckets.length > 1 ? buckets[buckets.length - 1].start - buckets[0].start : 0;

  const pick = (locationX: number) => {
    if (buckets.length === 0 || plotWidth <= 0) return;
    const at = buckets.length === 1 ? 0 : Math.round(((locationX - CHART_LEFT) / plotWidth) * (buckets.length - 1));
    setSelected(Math.min(buckets.length - 1, Math.max(0, at)));
  };

  const steps: number[] = [];
  for (let value = 0; value <= axis.top + axis.step / 2; value += axis.step) steps.push(value);
  const marks = buckets.length > 0 ? [0, Math.floor((buckets.length - 1) / 2), buckets.length - 1] : [];
  const bucket = selected !== null ? buckets[selected] : undefined;

  return (
    <View style={styles.chartCard}>
      <View
        onLayout={(event: LayoutChangeEvent) => setWidth(event.nativeEvent.layout.width)}
        onStartShouldSetResponder={() => true}
        onMoveShouldSetResponder={() => true}
        onResponderGrant={(event) => pick(event.nativeEvent.locationX)}
        onResponderMove={(event) => pick(event.nativeEvent.locationX)}
        accessibilityLabel={`${unitAdjective(unit)} ${cost ? "cost" : "tokens"} chart`}
      >
        {width > 0 ? (
          <Svg width={width} height={CHART_HEIGHT}>
            {steps.map((value) => (
              <Line key={`g${value}`} x1={CHART_LEFT} x2={width - CHART_RIGHT} y1={y(value)} y2={y(value)}
                stroke={color.line} strokeWidth={1} />
            ))}
            {steps.map((value) => (
              <SvgText key={`l${value}`} x={CHART_LEFT - 6} y={y(value) + 3.5} fontSize={scaled(9.5)} fill={color.muted}
                textAnchor="end" fontFamily={font.sans.fontFamily}>
                {cost ? axisMoney(value, axis.step) : tokens(value)}
              </SvgText>
            ))}
            {[...kinds].reverse().map((kind) => {
              const points = buckets.map((bucket, index) => ({ x: x(index), y: y(bucketValue(bucket, kind, metric)) }));
              const line = monotonePath(points);
              if (!line) return null;
              const area = `${line}L${x(buckets.length - 1)},${y(0)}L${x(0)},${y(0)}Z`;
              const tint = agentColor(kind, dark);
              return (
                <G key={kind}>
                  <Path d={area} fill={tint} fillOpacity={0.09} />
                  <Path d={line} stroke={tint} strokeWidth={1.6} fill="none" strokeLinejoin="round" strokeLinecap="round" />
                </G>
              );
            })}
            {bucket ? (
              <Line x1={x(selected!)} x2={x(selected!)} y1={CHART_TOP} y2={CHART_TOP + plotHeight}
                stroke={color.muted} strokeOpacity={0.6} strokeWidth={1} />
            ) : null}
            {marks.map((index, place) => (
              <SvgText key={`x${index}-${place}`} x={x(index)} y={CHART_HEIGHT - 5} fontSize={scaled(9.5)} fill={color.muted}
                textAnchor={place === 0 ? "start" : place === 2 ? "end" : "middle"} fontFamily={font.sans.fontFamily}>
                {axisLabel(unit, new Date(buckets[index].start), span)}
              </SvgText>
            ))}
          </Svg>
        ) : <View style={{ height: CHART_HEIGHT }} />}
      </View>
      <View style={styles.readout}>
        {bucket ? (
          <>
            <Text style={styles.readoutTitle}>{momentLabel(unit, new Date(bucket.start))}</Text>
            {kinds.map((kind) => (
              <View key={kind} style={styles.readoutRow}>
                <AgentIcon kind={kind} size={13} />
                <Text style={styles.readoutName}>{agentLabel(kind).name}</Text>
                <Text style={styles.readoutValue}>{format(bucketValue(bucket, kind, metric))}</Text>
              </View>
            ))}
            <View style={styles.readoutRule} />
            <View style={styles.readoutRow}>
              <Text style={styles.readoutName}>Total</Text>
              <Text style={styles.readoutValue}>{format(kinds.reduce((sum, kind) => sum + bucketValue(bucket, kind, metric), 0))}</Text>
            </View>
          </>
        ) : (
          <Text style={styles.hint}>Touch the chart to read out any {unitNoun(unit).toLowerCase()}.</Text>
        )}
      </View>
    </View>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  const styles = useStyles(makeStyles);
  return (
    <View style={styles.stat}>
      <Text style={styles.statLabel}>{label}</Text>
      <Text style={styles.statValue}>{value}</Text>
    </View>
  );
}

function ByKind({ usage, metric, dark }: { usage: Usage; metric: Metric; dark: boolean }) {
  const styles = useStyles(makeStyles);
  const cost = metric === "cost";
  const source = cost ? usage.cost : usage.tokens;
  const colors = kindColors(dark);
  const kinds = [
    { name: "Input", value: source.input, color: colors.input },
    { name: "Cache read", value: source.cacheRead, color: colors.cacheRead },
    { name: "Cache write", value: source.cacheWrite, color: colors.cacheWrite },
    { name: "Output", value: source.output, color: colors.output },
  ];
  const sum = kinds.reduce((total, kind) => total + kind.value, 0);
  return (
    <View style={styles.kinds}>
      <View style={styles.kindBar}>
        {kinds.filter((kind) => kind.value > 0).map((kind) => (
          <View key={kind.name} style={[styles.kindPart, { flexGrow: Math.max(kind.value / Math.max(sum, 1e-9), 0.015), backgroundColor: kind.color }]} />
        ))}
      </View>
      <View style={styles.legend}>
        {kinds.map((kind) => (
          <View key={kind.name} style={styles.legendItem}>
            <View style={[styles.swatch, { backgroundColor: kind.color }]} />
            <Text style={styles.legendName}>{kind.name}</Text>
            <Text style={styles.legendValue}>{cost ? money(kind.value) : tokens(kind.value)}</Text>
          </View>
        ))}
      </View>
    </View>
  );
}

/** Each model's part, or each bucket's, with a line as long as its share. */
function Breakdown({ usage, metric, by, dark }: {
  usage: Usage; metric: Metric; by: "model" | "bucket"; dark: boolean;
}) {
  const styles = useStyles(makeStyles);
  const cost = metric === "cost";
  const unit = unitOf(usage);
  const all = (t: Usage["tokens"]) => t.input + t.cacheRead + t.cacheWrite + t.output;
  const grand = cost ? all(usage.cost) : all(usage.tokens);
  const kinds = (usage.agents ?? []).map((agent) => agent.kind);
  const rows = by === "model"
    ? [...(usage.models ?? [])]
        .sort((a, b) => cost ? (b.cost - a.cost) || (all(b.tokens) - all(a.tokens)) : all(b.tokens) - all(a.tokens))
        .map((model) => ({
          key: `${model.kind}/${model.model}`, name: model.model || "Unknown model", kind: model.kind as string | undefined,
          cost: model.priced ? model.cost : undefined, tokens: all(model.tokens),
          parts: [] as { kind: string; value: number }[],
        }))
    : [...(usage.buckets ?? [])].reverse().flatMap((bucket) => {
        const count = Object.values(bucket.tokens ?? {}).reduce((sum, value) => sum + value, 0);
        if (count <= 0) return [];
        return [{
          key: String(bucket.start), name: rowLabel(unit, new Date(bucket.start)), kind: undefined as string | undefined,
          cost: Object.values(bucket.cost ?? {}).reduce((sum, value) => sum + value, 0) as number | undefined, tokens: count,
          parts: kinds.map((kind) => ({ kind, value: bucketValue(bucket, kind, metric) })),
        }];
      });
  const shareOf = (row: (typeof rows)[number]) => fraction(cost ? row.cost ?? 0 : row.tokens, grand);
  const most = Math.max(0, ...rows.map(shareOf));

  return (
    <View>
      {rows.map((row, index) => {
        const share = shareOf(row);
        const length = `${Math.max((share / Math.max(most, 1e-9)) * 100, 2)}%` as const;
        const partsSum = row.parts.reduce((sum, part) => sum + part.value, 0);
        return (
          <View key={row.key}>
            {index > 0 ? <View style={styles.divider} /> : null}
            <View style={styles.breakdownRow}>
              <Text style={styles.rank}>{index + 1}</Text>
              <View style={styles.breakdownMain}>
                <View style={styles.breakdownName}>
                  {row.kind ? <AgentIcon kind={row.kind} size={14} /> : null}
                  <Text style={styles.modelName} numberOfLines={1}>{row.name}</Text>
                </View>
                <View style={[styles.shareTrack, { width: length }]}>
                  {row.kind ? (
                    <View style={[styles.sharePart, { flexGrow: 1, backgroundColor: agentColor(row.kind, dark) }]} />
                  ) : row.parts.filter((part) => part.value > 0).map((part) => (
                    <View key={part.kind} style={[styles.sharePart, { flexGrow: part.value / Math.max(partsSum, 1e-9), backgroundColor: agentColor(part.kind, dark) }]} />
                  ))}
                </View>
              </View>
              <View style={styles.breakdownNumbers}>
                <Text style={styles.breakdownValue}>
                  {cost ? (row.cost === undefined ? "—" : money(row.cost)) : tokens(row.tokens)}
                </Text>
                <Text style={styles.breakdownDetail}>
                  {cost && row.cost === undefined ? "No known price" : `${percent(share)} · ${cost ? tokens(row.tokens) : row.cost === undefined ? "—" : money(row.cost)}`}
                </Text>
              </View>
            </View>
          </View>
        );
      })}
      {rows.length === 0 ? <Text style={[styles.hint, { paddingVertical: space.lg }]}>Nothing to break down.</Text> : null}
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    iconButton: { width: 36, height: 36, alignItems: "center", justifyContent: "center" },
    controls: { gap: space.sm, marginTop: space.md },
    metric: { width: 200 },
    note: { ...font.sans, fontSize: size.caption, color: c.muted, lineHeight: size.caption * 1.4 },
    skipped: { marginTop: space.lg },
    link: { ...font.sansMedium, fontSize: size.caption, color: c.workingText, marginTop: space.sm },
    problem: { marginTop: space.xl },
    loading: { marginTop: space.xxl, alignItems: "center", gap: space.md },
    summary: { marginTop: space.xl, marginBottom: space.xs },
    total: { ...font.sansBold, fontSize: size.display, color: c.text, fontVariant: ["tabular-nums"] },
    subtle: { ...font.sans, fontSize: size.caption, color: c.muted, marginTop: 2 },
    divider: { height: StyleSheet.hairlineWidth, backgroundColor: c.line },
    agentRow: { paddingVertical: space.md, gap: 3 },
    agentLine: { flexDirection: "row", alignItems: "center", gap: 7 },
    dot: { width: 7, height: 7, borderRadius: 4 },
    agentName: { ...font.sansMedium, fontSize: size.body, color: c.text, flexShrink: 1 },
    agentSessions: { ...font.sans, fontSize: size.label, color: c.muted, flexShrink: 2 },
    agentValue: { ...font.sansMedium, fontSize: size.body, color: c.text, marginLeft: "auto", fontVariant: ["tabular-nums"] },
    agentShare: { ...font.sans, fontSize: size.label, color: c.muted, marginLeft: 14 },
    chartCard: { paddingTop: space.md, paddingBottom: space.md },
    readout: { marginTop: space.sm, paddingTop: space.sm, borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: c.line, gap: 4 },
    readoutTitle: { ...font.sansMedium, fontSize: size.label, color: c.muted, marginBottom: 2 },
    readoutRow: { flexDirection: "row", alignItems: "center", gap: 6 },
    readoutName: { ...font.sans, fontSize: size.caption, color: c.textSecondary, flex: 1 },
    readoutValue: { ...font.sansMedium, fontSize: size.caption, color: c.text, fontVariant: ["tabular-nums"] },
    readoutRule: { height: StyleSheet.hairlineWidth, backgroundColor: c.line, marginVertical: 2 },
    hint: { ...font.sans, fontSize: size.label, color: c.faint },
    stats: { flexDirection: "row", flexWrap: "wrap", paddingVertical: space.md, rowGap: space.md },
    stat: { width: "50%", gap: 2 },
    statLabel: { ...font.sans, fontSize: size.label, color: c.muted },
    statValue: { ...font.sansMedium, fontSize: size.title, color: c.text, fontVariant: ["tabular-nums"] },
    kinds: { paddingVertical: space.lg, gap: space.md },
    kindBar: { flexDirection: "row", height: 8, gap: 3 },
    kindPart: { borderRadius: 4, flexBasis: 0, minWidth: 6 },
    legend: { flexDirection: "row", flexWrap: "wrap", columnGap: space.lg, rowGap: space.sm },
    legendItem: { flexDirection: "row", alignItems: "center", gap: 6 },
    swatch: { width: 9, height: 9, borderRadius: 2 },
    legendName: { ...font.sans, fontSize: size.label, color: c.muted },
    legendValue: { ...font.sansMedium, fontSize: size.label, color: c.text, fontVariant: ["tabular-nums"] },
    breakdownHead: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginTop: space.lg, marginBottom: space.xxs },
    breakdownSwitch: { width: 180 },
    sectionTitle: { ...font.sansBold, fontSize: size.caption, color: c.muted, marginLeft: space.xxs },
    card: { backgroundColor: c.surface, borderRadius: radius.xl, paddingHorizontal: space.lg, borderWidth: 1, borderColor: c.line },
    breakdownRow: { flexDirection: "row", alignItems: "center", paddingVertical: space.md, gap: space.sm },
    rank: { ...font.sans, fontSize: size.label, color: c.muted, width: 18, fontVariant: ["tabular-nums"] },
    breakdownMain: { flex: 1, gap: 6, minWidth: 0 },
    breakdownName: { flexDirection: "row", alignItems: "center", gap: 6 },
    modelName: { ...font.sans, fontSize: size.caption, color: c.text, flexShrink: 1 },
    shareTrack: { height: 2.5, borderRadius: 2, overflow: "hidden", flexDirection: "row" },
    sharePart: { height: 2.5, flexBasis: 0 },
    breakdownNumbers: { alignItems: "flex-end", gap: 2 },
    breakdownValue: { ...font.sansMedium, fontSize: size.caption, color: c.text, fontVariant: ["tabular-nums"] },
    breakdownDetail: { ...font.sans, fontSize: size.label, color: c.muted, fontVariant: ["tabular-nums"] },
  });
