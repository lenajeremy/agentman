import { useEffect, useState } from "react";
import { StyleSheet, Text, View } from "react-native";

import { AgentIcon } from "./AgentIcon";
import { useStyles, useTheme } from "../lib/appearance";
import {
  checkedLabel, evenLeft, leftPercent, LIMITS_FOOTNOTE, orderedWindows, pace, PACE_TITLE, resetsLabel,
} from "../lib/limits";
import type { AgentLimits, Limits, LimitWindow } from "../lib/protocol";
import { agentLabel, font, type Palette, radius, scaled, size, space } from "../lib/theme";
import { agentColor } from "../lib/usage";

/**
 * What is left of each agent's plan, as the Mac's Limits tab shows it: for
 * each agent, a row per window with the share left, a bar of it in the
 * agent's colour, a mark where spending evenly would have it, and when it
 * resets. The numbers are each agent's own: Claude Code's /usage, Codex's
 * /status.
 */
export function LimitsPage({ limits, now: fixed }: { limits: Limits; now?: number }) {
  const styles = useStyles(makeStyles);
  // The resets count down while the page is open.
  const [ticking, setTicking] = useState(() => Date.now());
  useEffect(() => {
    if (fixed !== undefined) return;
    const timer = setInterval(() => setTicking(Date.now()), 30_000);
    return () => clearInterval(timer);
  }, [fixed]);
  const now = fixed ?? ticking;

  if (limits.agents.length === 0) {
    return (
      <View style={styles.empty}>
        <Text style={styles.emptyTitle}>No plan limits to show</Text>
        <Text style={styles.note}>{LIMITS_FOOTNOTE}</Text>
      </View>
    );
  }
  return (
    <View>
      {limits.agents.map((agent) => <AgentSection key={agent.kind} agent={agent} now={now} />)}
      <Text style={styles.footnote}>{LIMITS_FOOTNOTE}</Text>
    </View>
  );
}

function AgentSection({ agent, now }: { agent: AgentLimits; now: number }) {
  const styles = useStyles(makeStyles);
  const { scheme } = useTheme();
  const windows = orderedWindows(agent.windows ?? []);
  const tint = agentColor(agent.kind, scheme === "dark");
  return (
    <View>
      <View style={styles.head}>
        <AgentIcon kind={agent.kind} size={16} />
        <Text style={styles.agentName} numberOfLines={1}>{agentLabel(agent.kind).name}</Text>
        {agent.plan ? <Text style={styles.plan}>{agent.plan}</Text> : null}
        <Text style={styles.checked} numberOfLines={1}>{checkedLabel(agent, now)}</Text>
      </View>
      {agent.problem ? <Text style={[styles.note, styles.problem]}>{agent.problem}</Text> : null}
      {windows.length > 0 ? (
        <View style={styles.card}>
          {windows.map((window, index) => (
            <View key={window.id}>
              {index > 0 ? <View style={styles.divider} /> : null}
              <WindowRow window={window} tint={tint} now={now} />
            </View>
          ))}
        </View>
      ) : null}
    </View>
  );
}

function WindowRow({ window, tint, now }: { window: LimitWindow; tint: string; now: number }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const left = leftPercent(window);
  const even = evenLeft(window, now);
  const resets = resetsLabel(window, now);
  const speed = pace(window, now);
  return (
    <View style={styles.row} accessible
      accessibilityLabel={`${window.label}: ${left}% left${resets ? `, ${resets.toLowerCase()}` : ""}${speed ? `, ${PACE_TITLE[speed].toLowerCase()}` : ""}`}>
      <View style={styles.rowHead}>
        <Text style={styles.label} numberOfLines={1}>{window.label}</Text>
        <Text style={[styles.left, left === 0 && { color: color.errorText }]}>
          {left}%<Text style={styles.leftWord}> left</Text>
        </Text>
      </View>
      <View style={styles.track}>
        <View style={[styles.fill, { width: `${left}%`, backgroundColor: tint }]} />
        {even !== null ? <View style={[styles.tick, { left: `${Math.min(Math.max(even, 1), 99)}%` }]} /> : null}
      </View>
      <View style={styles.rowFoot}>
        <Text style={styles.caption}>{resets ?? (left === 100 ? "Not used yet" : " ")}</Text>
        {speed ? (
          <Text style={[styles.caption, speed === "ahead" && { color: color.needsYouText }]}>{PACE_TITLE[speed]}</Text>
        ) : null}
      </View>
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    head: { flexDirection: "row", alignItems: "center", gap: 7, marginTop: space.xl, marginBottom: space.sm, marginHorizontal: space.xxs },
    agentName: { ...font.sansBold, fontSize: size.body, color: c.text, flexShrink: 1 },
    plan: {
      ...font.sansMedium, fontSize: size.label, color: c.textSecondary, backgroundColor: c.fill,
      borderRadius: radius.pill, paddingHorizontal: 7, paddingVertical: 1, overflow: "hidden",
    },
    checked: { ...font.sans, fontSize: size.label, color: c.muted, marginLeft: "auto", flexShrink: 1 },
    problem: { marginBottom: space.sm, marginHorizontal: space.xxs },
    card: { backgroundColor: c.surface, borderRadius: radius.xl, paddingHorizontal: space.lg, borderWidth: 1, borderColor: c.line },
    divider: { height: StyleSheet.hairlineWidth, backgroundColor: c.line },
    row: { paddingVertical: space.md, gap: space.sm },
    rowHead: { flexDirection: "row", alignItems: "baseline", justifyContent: "space-between", gap: space.sm },
    label: { ...font.sansMedium, fontSize: size.caption, color: c.textSecondary, flexShrink: 1 },
    left: { ...font.sansBold, fontSize: size.title, color: c.text, fontVariant: ["tabular-nums"] },
    leftWord: { ...font.sans, fontSize: size.label, color: c.muted },
    track: { height: scaled(10), borderRadius: 5, backgroundColor: c.fill, overflow: "hidden" },
    fill: { position: "absolute", left: 0, top: 0, bottom: 0, borderRadius: 5, opacity: 0.45 },
    tick: { position: "absolute", top: 1, bottom: 1, width: 2, marginLeft: -1, borderRadius: 1, backgroundColor: c.text },
    rowFoot: { flexDirection: "row", justifyContent: "space-between", gap: space.sm },
    caption: { ...font.sans, fontSize: size.label, color: c.muted, fontVariant: ["tabular-nums"] },
    note: { ...font.sans, fontSize: size.caption, color: c.muted, lineHeight: size.caption * 1.4 },
    footnote: { ...font.sans, fontSize: size.label, lineHeight: scaled(17), color: c.faint, marginTop: space.lg, marginHorizontal: space.xs },
    empty: { marginTop: space.xxl, alignItems: "center", gap: space.sm, paddingHorizontal: space.lg },
    emptyTitle: { ...font.sansMedium, fontSize: size.title, color: c.text },
  });
