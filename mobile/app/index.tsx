import { Redirect, useRouter } from "expo-router";
import { useEffect, useMemo, useState } from "react";
import {
  RefreshControl,
  SectionList,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { Appear } from "../components/Appear";
import { ContentColumn } from "../components/ContentColumn";
import { MotionPressable } from "../components/MotionPressable";
import { Pulse } from "../components/Pulse";
import { QuestionCard } from "../components/QuestionCard";
import { SwipeToDismiss } from "../components/SwipeToDismiss";
import { canDismiss } from "../lib/dismissed";
import { Session } from "../lib/protocol";
import { sessionNeedsAnswer } from "../lib/question-alerts";
import { useStore } from "../lib/store";
import { ago, color, font, radius, shortPath, size, space, stateStyle } from "../lib/theme";

export default function Agents() {
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const [refreshing, setRefreshing] = useState(false);
  /** The row just swiped away, offered back for a few seconds. */
  const [undo, setUndo] = useState<{ id: string; name: string } | null>(null);
  // Relative times ("3m") go stale while the screen sits open.
  const [, forceTick] = useState(0);

  useEffect(() => {
    const timer = setInterval(() => forceTick((n) => n + 1), 10_000);
    return () => clearInterval(timer);
  }, []);

  useEffect(() => {
    if (!undo) return;
    const timer = setTimeout(() => setUndo(null), 5000);
    return () => clearTimeout(timer);
  }, [undo]);

  const groups = useMemo(
    () => groupByState(store.visibleSessions),
    [store.visibleSessions],
  );
  const hiddenCount = store.sessions.length - store.visibleSessions.length;
  const incompatible = store.connection === "incompatible";

  if (!store.ready) return <View style={styles.page} />;
  if (!store.credentials) return <Redirect href="/pair" />;

  const counts = store.visibleSessions.reduce(
    (current, session) => {
      if (sessionNeedsAnswer(session)) current.needsYou += 1;
      else if (session.state === "busy") current.working += 1;
      else current.idle += 1;
      return current;
    },
    { needsYou: 0, working: 0, idle: 0 },
  );

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      <ContentColumn style={styles.header}>
        <View>
          <Text style={styles.wordmark}>
            agentman<Text style={styles.wordmarkAccent}>.</Text>
          </Text>
          <View style={styles.connectionLine}>
            <View
              style={[
                styles.connectionDot,
                { backgroundColor: store.daemonOnline ? color.ok : color.error },
              ]}
            />
            <Text style={styles.subhead}>
              {store.daemonOnline
                ? "Mac connected"
                : incompatible
                  ? "Update required"
                  : "Trying to reconnect"}
            </Text>
          </View>
        </View>
        <MotionPressable
          hitSlop={12}
          style={styles.gear}
          pressedScale={0.94}
          onPress={() => router.push("/settings")}
          accessibilityRole="button"
          accessibilityLabel="Settings"
        >
          <SettingsGlyph />
        </MotionPressable>
      </ContentColumn>

      {!store.daemonOnline && (
        <ContentColumn>
          {incompatible
            ? <ProtocolBanner />
            : <OfflineBanner lastSeenAt={store.lastSeenAt} />}
        </ContentColumn>
      )}

      <SectionList
        style={styles.scroll}
        sections={groups.map((group) => ({
          ...group,
          data: group.sessions,
        }))}
        keyExtractor={(session) => session.id}
        stickySectionHeadersEnabled={false}
        initialNumToRender={12}
        windowSize={7}
        contentContainerStyle={{ paddingBottom: insets.bottom + space.xxl }}
        refreshControl={
          <RefreshControl
            refreshing={refreshing}
            tintColor={color.faint}
            onRefresh={() => {
              setRefreshing(true);
              store.refresh();
              setTimeout(() => setRefreshing(false), 600);
            }}
          />
        }
        ListHeaderComponent={
          <ContentColumn>
            {store.visibleSessions.length > 0 ? (
            <Appear style={styles.summaryWrap}>
              <DashboardSummary counts={counts} />
            </Appear>
            ) : null}

            {store.visibleSessions.length === 0 && store.daemonOnline &&
              (hiddenCount > 0 ? (
                <AllHiddenState count={hiddenCount} onShow={store.restoreAllSessions} />
              ) : (
                <EmptyState />
              ))}
          </ContentColumn>
        }
        renderSectionHeader={({ section }) => (
          <ContentColumn>
            <View style={styles.groupHeading}>
              <Text style={[styles.groupLabel, { color: section.tint }]}>
                {section.label}
              </Text>
              <Text style={styles.groupCount}>{section.data.length}</Text>
            </View>
          </ContentColumn>
        )}
        renderItem={({ item: session }) => (
          <ContentColumn>
            <SwipeToDismiss
              enabled={canDismiss(session)}
              onDismiss={() => {
                store.dismissSession(session.id);
                setUndo({ id: session.id, name: session.name });
              }}
              accessibilityLabel={`${session.name}, ${stateStyle(effectiveSessionState(session)).label}`}
            >
              <AgentRow session={session} />
            </SwipeToDismiss>
          </ContentColumn>
        )}
      />

      {undo && (
        <Appear
          key={undo.id}
          style={[styles.undoBar, { bottom: insets.bottom + space.lg }]}
          offset={8}
        >
          <Text style={styles.undoText} numberOfLines={1}>
            Hid {undo.name}
          </Text>
          <MotionPressable
            hitSlop={10}
            style={styles.undoButton}
            pressedScale={0.94}
            onPress={() => {
              store.restoreSession(undo.id);
              setUndo(null);
            }}
            accessibilityRole="button"
          >
            <Text style={styles.undoAction}>Undo</Text>
          </MotionPressable>
        </Appear>
      )}
    </View>
  );
}

/**
 * Grouping by state rather than one flat list.
 *
 * The question this screen answers is "does anything need me?", so the answer
 * is the structure: blocked agents form their own section at the top, and the
 * section header is the answer rather than a decoration.
 */
function groupByState(sessions: Session[]) {
  const buckets = new Map<
    string,
    { label: string; tint: string; rank: number; sessions: Session[] }
  >();
  for (const session of sessions) {
    const meta = stateStyle(effectiveSessionState(session));
    const bucket = buckets.get(meta.label) ?? {
      label: meta.label,
      tint: meta.rank === 0 ? meta.color : color.faint,
      rank: meta.rank,
      sessions: [],
    };
    bucket.sessions.push(session);
    buckets.set(meta.label, bucket);
  }
  return Array.from(buckets.values())
    .map((bucket) => ({
      ...bucket,
      sessions: bucket.sessions.sort((a, b) => b.lastActivityAt - a.lastActivityAt),
    }))
    .sort((a, b) => a.rank - b.rank);
}

function AgentRow({ session }: { session: Session }) {
  const router = useRouter();
  const needsYou = sessionNeedsAnswer(session);
  const displayState = effectiveSessionState(session);

  return (
    <MotionPressable
      onPress={() => router.push(`/session/${encodeURIComponent(session.id)}`)}
      style={[styles.row, needsYou && styles.rowNeedsYou]}
      pressedScale={0.985}
      accessibilityRole="button"
      accessibilityLabel={`${session.name}, ${stateStyle(displayState).label}`}
    >
      {needsYou ? <View style={styles.priorityRail} /> : null}
      <View style={styles.pulseWrap}>
        <Pulse state={displayState} />
      </View>
      <View style={styles.rowBody}>
        <View style={styles.rowTop}>
          {/* Session names are machine-generated, so they are set in mono —
              the same signal used for paths, ids, and commands. */}
          <Text style={styles.name} numberOfLines={1}>
            {session.name}
          </Text>
          <Text style={styles.age}>{ago(session.lastActivityAt)}</Text>
        </View>
        {/* Path and model share a line so the row does not grow a third one.
            Both are machine-authored, so both are set in mono. */}
        <View style={styles.rowMeta}>
          <Text style={styles.path} numberOfLines={1}>
            {shortPath(session.cwd)}
          </Text>
          <View style={styles.modelBadge}>
            <Text style={styles.model} numberOfLines={1}>
              {session.model ?? session.kind}
            </Text>
          </View>
        </View>
        {session.question ? (
          <QuestionCard
            question={session.question}
            onAnswer={() => {}}
            compact
          />
        ) : needsYou ? (
          <Text style={styles.needsYouNote}>Waiting on your answer</Text>
        ) : null}
      </View>
      <Text style={styles.chevron}>›</Text>
    </MotionPressable>
  );
}

function effectiveSessionState(session: Session): Session["state"] {
  return sessionNeedsAnswer(session) ? "waiting_input" : session.state;
}

function DashboardSummary({
  counts,
}: {
  counts: { needsYou: number; working: number; idle: number };
}) {
  const total = counts.needsYou + counts.working + counts.idle;
  const title = counts.needsYou
    ? `${counts.needsYou} ${counts.needsYou === 1 ? "agent needs" : "agents need"} you`
    : counts.working
      ? `${counts.working} ${counts.working === 1 ? "agent is" : "agents are"} working`
      : "Everything is caught up";
  const detail = counts.needsYou
    ? "A decision is blocking progress."
    : counts.working
      ? "You can step away — status updates live."
      : `${total} ${total === 1 ? "agent is" : "agents are"} ready when you are.`;

  return (
    <View style={[styles.summary, counts.needsYou > 0 && styles.summaryAttention]}>
      <View style={styles.summaryTop}>
        <View style={styles.summaryCopy}>
          <Text style={styles.eyebrow}>Live workspace</Text>
          <Text style={styles.summaryTitle}>{title}</Text>
          <Text style={styles.summaryDetail}>{detail}</Text>
        </View>
        <View
          style={[
            styles.summarySignal,
            {
              backgroundColor: counts.needsYou ? color.needsYouWash : color.workingWash,
            },
          ]}
        >
          <Pulse
            state={counts.needsYou ? "waiting_input" : counts.working ? "busy" : "idle"}
            size={9}
          />
        </View>
      </View>
      <View style={styles.metrics}>
        <Metric label="Needs you" value={counts.needsYou} tint={color.needsYou} />
        <View style={styles.metricDivider} />
        <Metric label="Working" value={counts.working} tint={color.working} />
        <View style={styles.metricDivider} />
        <Metric label="Idle" value={counts.idle} tint={color.muted} />
      </View>
    </View>
  );
}

function Metric({
  label,
  value,
  tint,
}: {
  label: string;
  value: number;
  tint: string;
}) {
  return (
    <View style={styles.metric}>
      <Text style={[styles.metricValue, { color: tint }]}>{value}</Text>
      <Text style={styles.metricLabel}>{label}</Text>
    </View>
  );
}

function SettingsGlyph() {
  return (
    <View style={styles.settingsGlyph}>
      <View style={styles.settingsLine}>
        <View style={[styles.settingsKnob, { left: 5 }]} />
      </View>
      <View style={styles.settingsLine}>
        <View style={[styles.settingsKnob, { right: 5 }]} />
      </View>
      <View style={styles.settingsLine}>
        <View style={[styles.settingsKnob, { left: 9 }]} />
      </View>
    </View>
  );
}

/**
 * Shown when every agent has been swiped away, instead of "Nothing running".
 *
 * Those two states look identical and mean opposite things: one is an idle
 * machine, the other is a machine full of agents you cannot see. Without this
 * the app would flatly lie about the second.
 */
function AllHiddenState({ count, onShow }: { count: number; onShow(): void }) {
  return (
    <View style={styles.empty}>
      <Text style={styles.emptyTitle}>All caught up</Text>
      <Text style={styles.emptyBody}>
        {count} idle {count === 1 ? "agent is" : "agents are"} hidden. They come
        back on their own the moment anything happens.
      </Text>
      {/* The only way back to a session hidden long ago, and it appears only
          here — on a board that is empty solely because of hiding. */}
      <MotionPressable
        hitSlop={10}
        style={styles.emptyActionButton}
        onPress={onShow}
        accessibilityRole="button"
      >
        <Text style={styles.emptyAction}>Show them anyway</Text>
      </MotionPressable>
    </View>
  );
}

function OfflineBanner({ lastSeenAt }: { lastSeenAt: number | null }) {
  return (
    <View style={styles.banner}>
      <View style={styles.offlineIcon}>
        <Text style={styles.offlineGlyph}>!</Text>
      </View>
      <View style={styles.bannerCopy}>
        <Text style={styles.bannerText}>
          Your Mac is offline{lastSeenAt ? ` · last seen ${ago(lastSeenAt)} ago` : ""}
        </Text>
        <Text style={styles.bannerHint}>Agents appear here when it reconnects.</Text>
      </View>
    </View>
  );
}

function ProtocolBanner() {
  return (
    <View style={styles.banner} accessibilityRole="alert">
      <View style={styles.offlineIcon}>
        <Text style={styles.offlineGlyph}>!</Text>
      </View>
      <View style={styles.bannerCopy}>
        <Text style={styles.bannerText}>Agentman versions do not match</Text>
        <Text style={styles.bannerHint}>
          Update or restart the app, daemon, and relay together.
        </Text>
      </View>
    </View>
  );
}

function EmptyState() {
  return (
    <View style={styles.empty}>
      <View style={styles.emptyIcon}>
        <Text style={styles.emptyGlyph}>›_</Text>
      </View>
      <Text style={styles.emptyTitle}>Nothing running</Text>
      <Text style={styles.emptyBody}>
        Start an agent on your Mac and it shows up here. Use{" "}
        <Text style={styles.code}>am claude</Text> to start one you can message back.
      </Text>
    </View>
  );
}

const styles = StyleSheet.create({
  page: { flex: 1, backgroundColor: color.ink },
  scroll: { flex: 1 },

  header: {
    flexDirection: "row",
    alignItems: "flex-start",
    justifyContent: "space-between",
    paddingHorizontal: space.lg,
    paddingTop: space.lg,
    paddingBottom: space.lg,
  },
  wordmark: {
    fontFamily: font.mono,
    fontSize: size.display,
    color: color.text,
    letterSpacing: -1.2,
  },
  wordmarkAccent: { color: color.working },
  connectionLine: { flexDirection: "row", alignItems: "center", gap: 6, marginTop: 3 },
  connectionDot: { width: 6, height: 6, borderRadius: 3 },
  subhead: {
    fontFamily: font.sans,
    fontSize: size.caption,
    color: color.muted,
  },
  gear: {
    width: 44,
    height: 44,
    borderRadius: radius.pill,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: color.surface,
    borderWidth: 1,
    borderColor: color.line,
  },
  settingsGlyph: { width: 19, gap: 4 },
  settingsLine: { height: 2, borderRadius: 1, backgroundColor: color.muted },
  settingsKnob: {
    position: "absolute",
    top: -2,
    width: 6,
    height: 6,
    borderRadius: 3,
    backgroundColor: color.text,
  },

  summaryWrap: { marginHorizontal: space.lg, marginTop: space.xs },
  summary: {
    borderRadius: radius.xxl,
    padding: space.xl,
    backgroundColor: color.surface,
    borderWidth: 1,
    borderColor: color.line,
  },
  summaryAttention: { borderColor: "#594523", backgroundColor: color.needsYouWash },
  summaryTop: { flexDirection: "row", gap: space.md, alignItems: "flex-start" },
  summaryCopy: { flex: 1 },
  eyebrow: {
    fontFamily: font.sansMedium,
    fontSize: size.label,
    letterSpacing: 1.4,
    textTransform: "uppercase",
    color: color.faint,
  },
  summaryTitle: {
    fontFamily: font.sansBold,
    fontSize: size.heading,
    lineHeight: 27,
    letterSpacing: -0.35,
    color: color.text,
    marginTop: space.xs,
  },
  summaryDetail: {
    fontFamily: font.sans,
    fontSize: size.caption,
    lineHeight: 18,
    color: color.muted,
    marginTop: space.xs,
  },
  summarySignal: {
    width: 42,
    height: 42,
    borderRadius: radius.md,
    alignItems: "center",
    justifyContent: "center",
  },
  metrics: {
    flexDirection: "row",
    marginTop: space.lg,
    paddingTop: space.md,
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: color.line,
  },
  metric: { flex: 1, alignItems: "center", gap: space.xxs },
  metricValue: { fontFamily: font.monoMedium, fontSize: size.title },
  metricLabel: { fontFamily: font.sans, fontSize: size.label, color: color.faint },
  metricDivider: { width: StyleSheet.hairlineWidth, backgroundColor: color.line },

  groupHeading: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: space.lg,
    marginTop: space.xxl,
    marginBottom: space.md,
  },
  groupLabel: {
    fontFamily: font.sansMedium,
    fontSize: size.label,
    letterSpacing: 1.4,
    textTransform: "uppercase",
  },
  groupCount: { fontFamily: font.mono, fontSize: size.label, color: color.faint },

  row: {
    flexDirection: "row",
    alignItems: "flex-start",
    gap: space.sm,
    paddingVertical: space.lg,
    paddingHorizontal: space.lg,
    marginHorizontal: space.lg,
    borderRadius: radius.xxl,
    backgroundColor: color.surface,
    marginBottom: space.md,
    borderWidth: 1,
    borderColor: color.line,
    overflow: "hidden",
  },
  // The one place the design raises its voice: an agent that cannot continue
  // without the user.
  rowNeedsYou: { borderColor: "#594523", backgroundColor: color.needsYouWash },
  priorityRail: {
    position: "absolute",
    left: 0,
    top: 14,
    bottom: 14,
    width: 3,
    borderTopRightRadius: 2,
    borderBottomRightRadius: 2,
    backgroundColor: color.needsYou,
  },
  pulseWrap: { marginTop: -space.xs },

  rowBody: { flex: 1, gap: space.xxs },
  rowTop: { flexDirection: "row", alignItems: "baseline", gap: space.sm },
  name: {
    flex: 1,
    fontFamily: font.monoMedium,
    fontSize: size.body,
    color: color.text,
  },
  age: { fontFamily: font.sans, fontSize: size.caption, color: color.faint },
  rowMeta: { flexDirection: "row", alignItems: "center", gap: space.sm },
  path: { flex: 1, fontFamily: font.mono, fontSize: size.caption, color: color.muted },
  // The model, or the agent kind until the agent has replied once and named
  // one. Falling back to the kind rather than showing nothing keeps the column
  // from flickering into existence on the first reply.
  modelBadge: {
    maxWidth: "45%",
    paddingHorizontal: space.sm,
    paddingVertical: 3,
    borderRadius: radius.pill,
    backgroundColor: color.sunken,
    borderWidth: 1,
    borderColor: color.line,
  },
  model: { fontFamily: font.mono, fontSize: size.label, color: color.faint },
  chevron: {
    alignSelf: "center",
    color: color.faint,
    fontFamily: font.sans,
    fontSize: 22,
    lineHeight: 24,
  },
  needsYouNote: {
    fontFamily: font.sansMedium,
    fontSize: size.caption,
    color: color.needsYou,
    marginTop: space.xs,
  },

  banner: {
    flexDirection: "row",
    alignItems: "center",
    gap: space.md,
    marginHorizontal: space.lg,
    padding: space.md,
    borderRadius: radius.lg,
    backgroundColor: color.errorWash,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: "#563039",
  },
  offlineIcon: {
    width: 30,
    height: 30,
    borderRadius: 15,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: "#402127",
  },
  offlineGlyph: { color: color.error, fontFamily: font.sansBold, fontSize: size.body },
  bannerCopy: { flex: 1 },
  bannerText: { fontFamily: font.sansMedium, fontSize: size.caption, color: color.text },
  bannerHint: { fontFamily: font.sans, fontSize: size.caption, color: color.muted, marginTop: 2 },

  empty: {
    marginHorizontal: space.lg,
    marginTop: space.xl,
    paddingHorizontal: space.xl,
    paddingVertical: space.xxl,
    alignItems: "center",
    borderRadius: radius.xl,
    backgroundColor: color.surface,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.line,
    gap: space.sm,
  },
  emptyIcon: {
    width: 52,
    height: 52,
    borderRadius: radius.lg,
    backgroundColor: color.sunken,
    alignItems: "center",
    justifyContent: "center",
    marginBottom: space.sm,
  },
  emptyGlyph: { fontFamily: font.monoMedium, fontSize: size.title, color: color.working },
  emptyTitle: { fontFamily: font.sansBold, fontSize: size.heading, color: color.text },
  emptyBody: { fontFamily: font.sans, fontSize: size.body, color: color.muted, lineHeight: 22 },
  code: { fontFamily: font.mono, color: color.working },
  emptyAction: {
    fontFamily: font.sansMedium,
    fontSize: size.body,
    color: color.working,
  },
  emptyActionButton: { paddingVertical: space.sm, paddingHorizontal: space.md },

  undoBar: {
    position: "absolute",
    alignSelf: "center",
    width: "90%",
    maxWidth: 520,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: space.md,
    paddingVertical: space.md,
    paddingHorizontal: space.lg,
    borderRadius: radius.lg,
    backgroundColor: color.surfaceRaised,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.lineStrong,
    zIndex: 20,
  },
  undoText: { flex: 1, fontFamily: font.sans, fontSize: size.body, color: color.muted },
  undoAction: { fontFamily: font.sansMedium, fontSize: size.body, color: color.working },
  undoButton: { paddingVertical: space.xs, paddingHorizontal: space.sm },
});
