import Feather from "@expo/vector-icons/Feather";
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
import { AgentIcon } from "../components/AgentIcon";
import { ContentColumn } from "../components/ContentColumn";
import { EmptyIllustration } from "../components/Illustrations";
import { MotionPressable } from "../components/MotionPressable";
import { Pulse } from "../components/Pulse";
import { QuestionCard } from "../components/QuestionCard";
import { ROW_GAP, SwipeToDismiss } from "../components/SwipeToDismiss";
import { useStyles, useTheme } from "../lib/appearance";
import { canDismiss } from "../lib/dismissed";
import { Session } from "../lib/protocol";
import { sessionNeedsAnswer } from "../lib/question-alerts";
import { useStore } from "../lib/store";
import {
  agentLabel,
  ago,
  font,
  Palette,
  radius,
  shortPath,
  size,
  space,
  stateStyle,
} from "../lib/theme";

export default function Agents() {
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
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
    () => groupByState(store.visibleSessions, color),
    [store.visibleSessions, color],
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
          <ContentColumn style={styles.gutter}>
            <View style={styles.topBar}>
              <ConnectionChip
                online={store.daemonOnline}
                incompatible={incompatible}
              />
              <MotionPressable
                hitSlop={12}
                style={styles.iconButton}
                pressedScale={0.94}
                onPress={() => router.push("/settings")}
                accessibilityRole="button"
                accessibilityLabel="Settings"
              >
                <Feather name="sliders" size={17} color={color.text} />
              </MotionPressable>
            </View>

            <Text style={styles.title} accessibilityRole="header">
              Agents
            </Text>

            {!store.daemonOnline ? (
              incompatible ? <ProtocolBanner /> : <OfflineBanner lastSeenAt={store.lastSeenAt} />
            ) : null}

            {store.visibleSessions.length > 0 ? (
              <Appear style={styles.tiles}>
                <CountTile
                  value={counts.needsYou}
                  label="Needs you"
                  tint={color.needsYouText}
                  wash={color.needsYouWash}
                />
                <CountTile
                  value={counts.working}
                  label="Working"
                  tint={color.working}
                  wash={color.workingWash}
                />
                <CountTile value={counts.idle} label="Idle" tint={color.text} wash={color.fill} />
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
          <ContentColumn style={styles.gutter}>
            <View style={styles.groupHeading}>
              <Text style={[styles.groupLabel, { color: section.tint }]}>{section.label}</Text>
              <Text style={styles.groupCount}>{section.data.length}</Text>
            </View>
          </ContentColumn>
        )}
        renderItem={({ item: session }) => (
          <ContentColumn style={styles.gutter}>
            <SwipeToDismiss
              enabled={canDismiss(session)}
              onDismiss={() => {
                store.dismissSession(session.id);
                setUndo({ id: session.id, name: session.name });
              }}
              accessibilityLabel={`${session.name}, ${stateStyle(effectiveSessionState(session), color).label}`}
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
function groupByState(sessions: Session[], color: Palette) {
  const buckets = new Map<
    string,
    { label: string; tint: string; rank: number; sessions: Session[] }
  >();
  for (const session of sessions) {
    const meta = stateStyle(effectiveSessionState(session), color);
    const bucket = buckets.get(meta.label) ?? {
      label: meta.label,
      tint: meta.rank === 0 ? meta.text : color.muted,
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
  const store = useStore();
  const router = useRouter();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const needsYou = sessionNeedsAnswer(session);
  const displayState = effectiveSessionState(session);
  const agent = agentLabel(session.kind);
  const open = () => router.push(`/session/${encodeURIComponent(session.id)}`);

  if (session.question) {
    // The one row that raises its voice: an agent that cannot continue
    // without the user. Simple prompts can be answered right here.
    const answerAction = store.actions.find(
      (action) => action.sessionId === session.id && action.kind === "answer",
    );
    return (
      // Not one big pressable: that would fold the answer buttons into a single
      // element for VoiceOver. The header opens the session; the buttons answer.
      <View style={[styles.card, styles.askCard]}>
        <MotionPressable
          onPress={open}
          style={styles.askHeader}
          pressedScale={0.98}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel={`${session.name}, needs you. Open session`}
        >
          <View style={styles.pill}>
            <View style={[styles.pillDot, { backgroundColor: color.needsYou }]} />
            <Text style={styles.pillLabel}>Needs you</Text>
          </View>
          <Text style={styles.askName} numberOfLines={1}>
            {session.name}
          </Text>
          <Text style={styles.age}>{ago(session.lastActivityAt)}</Text>
          <Feather name="chevron-right" size={16} color={color.faint} />
        </MotionPressable>
        <QuestionCard
          question={session.question}
          onAnswer={(answer) => store.answerQuestion(session.id, answer)}
          onOpen={open}
          disabled={!store.daemonOnline}
          submissionStatus={answerAction?.status}
          submissionError={answerAction?.error}
          compact
        />
      </View>
    );
  }

  const busy = displayState === "busy";
  return (
    <MotionPressable
      onPress={open}
      style={[styles.card, styles.row, needsYou && styles.rowNeedsYou]}
      pressedScale={0.985}
      accessibilityRole="button"
      accessibilityLabel={`${session.name}, ${stateStyle(displayState, color).label}`}
    >
      <View
        style={[
          styles.avatar,
          busy && { backgroundColor: color.workingWash },
          needsYou && { backgroundColor: color.needsYouWash },
        ]}
      >
        <AgentIcon kind={session.kind} size={22} />
        {displayState !== "idle" && displayState !== "ended" ? (
          <View style={styles.avatarPulse}>
            <Pulse state={displayState} size={7} />
          </View>
        ) : null}
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
            The model, or the agent's name until it has replied once and named
            one, keeps the line from flickering into existence. */}
        <Text style={styles.meta} numberOfLines={1}>
          {busy ? <Text style={styles.metaWorking}>Working · </Text> : null}
          {shortPath(session.cwd)} · {session.model ?? agent.name}
        </Text>
        {session.servers?.length ? (
          <View style={styles.serversLine}>
            <Feather name="globe" size={11} color={color.ok} />
            <Text style={styles.serversText} numberOfLines={1}>
              {session.servers.map((server) => `:${server.port}`).join("  ")}
            </Text>
          </View>
        ) : null}
        {needsYou ? <Text style={styles.needsYouNote}>Waiting on your answer</Text> : null}
      </View>
      <Feather name="chevron-right" size={18} color={color.faint} />
    </MotionPressable>
  );
}

function effectiveSessionState(session: Session): Session["state"] {
  return sessionNeedsAnswer(session) ? "waiting_input" : session.state;
}

function ConnectionChip({ online, incompatible }: { online: boolean; incompatible: boolean }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <View style={styles.chip} accessibilityRole="text">
      <View
        style={[styles.chipDot, { backgroundColor: online ? color.ok : color.error }]}
      />
      <Text style={styles.chipText}>
        {online ? "Mac connected" : incompatible ? "Update required" : "Reconnecting"}
      </Text>
    </View>
  );
}

function CountTile({
  value,
  label,
  tint,
  wash,
}: {
  value: number;
  label: string;
  tint: string;
  wash: string;
}) {
  const styles = useStyles(makeStyles);
  return (
    <View style={[styles.tile, { backgroundColor: wash }]} accessible accessibilityLabel={`${value} ${label}`}>
      <Text style={[styles.tileValue, { color: tint }]}>{value}</Text>
      <Text style={styles.tileLabel}>{label}</Text>
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
  const styles = useStyles(makeStyles);
  return (
    <View style={styles.empty}>
      <EmptyIllustration style={styles.emptyArt} />
      <Text style={styles.emptyTitle}>All caught up</Text>
      <Text style={styles.emptyBody}>
        {count} idle {count === 1 ? "agent is" : "agents are"} hidden. They come
        back on their own the moment anything happens.
      </Text>
      {/* The only way back to a session hidden long ago, and it appears only
          here — on a board that is empty solely because of hiding. */}
      <MotionPressable
        hitSlop={10}
        style={styles.secondaryButton}
        onPress={onShow}
        accessibilityRole="button"
      >
        <Text style={styles.secondaryButtonText}>Show them anyway</Text>
      </MotionPressable>
    </View>
  );
}

function OfflineBanner({ lastSeenAt }: { lastSeenAt: number | null }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <View style={styles.banner} accessibilityRole="alert">
      <View style={styles.bannerIcon}>
        <Feather name="wifi-off" size={15} color={color.errorText} />
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
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <View style={styles.banner} accessibilityRole="alert">
      <View style={styles.bannerIcon}>
        <Feather name="alert-triangle" size={15} color={color.errorText} />
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
  const styles = useStyles(makeStyles);
  return (
    <View style={styles.empty}>
      <EmptyIllustration style={styles.emptyArt} />
      <Text style={styles.emptyTitle}>Nothing running</Text>
      <Text style={styles.emptyBody}>
        Start an agent on your Mac and it shows up here, ready to take your messages.
      </Text>
      <View style={styles.command}>
        <Text style={styles.commandPrompt}>$</Text>
        <Text style={styles.commandText} selectable>
          am claude
        </Text>
      </View>
      <Text style={styles.emptyHint}>
        Also <Text style={styles.inlineMono}>am codex</Text> and{" "}
        <Text style={styles.inlineMono}>am opencode</Text>
      </Text>
    </View>
  );
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    page: { flex: 1, backgroundColor: c.paper },
    scroll: { flex: 1 },
    gutter: { paddingHorizontal: space.lg },

    topBar: {
      flexDirection: "row",
      alignItems: "center",
      justifyContent: "space-between",
      paddingTop: space.md,
    },
    chip: {
      flexDirection: "row",
      alignItems: "center",
      gap: 7,
      height: 32,
      paddingHorizontal: space.md,
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
    },
    chipDot: { width: 7, height: 7, borderRadius: 4 },
    chipText: { fontFamily: font.sansMedium, fontSize: size.caption, color: c.textSecondary },
    iconButton: {
      width: 40,
      height: 40,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
    },
    title: {
      fontFamily: font.sansBold,
      fontSize: size.display,
      letterSpacing: -1.2,
      color: c.text,
      marginTop: space.lg,
    },

    tiles: { flexDirection: "row", gap: space.sm, marginTop: space.lg },
    tile: { flex: 1, borderRadius: radius.xl, paddingHorizontal: space.md, paddingVertical: space.md },
    tileValue: {
      fontFamily: font.sansBold,
      fontSize: 28,
      letterSpacing: -0.8,
      fontVariant: ["tabular-nums"],
    },
    tileLabel: { fontFamily: font.sansMedium, fontSize: size.caption, color: c.muted, marginTop: 2 },

    groupHeading: {
      flexDirection: "row",
      alignItems: "baseline",
      justifyContent: "space-between",
      paddingHorizontal: space.xxs,
      marginTop: space.xl,
      marginBottom: space.sm,
    },
    groupLabel: { fontFamily: font.sansBold, fontSize: size.caption },
    groupCount: { fontFamily: font.mono, fontSize: size.label, color: c.faint },

    card: {
      borderRadius: radius.xl,
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
      marginBottom: ROW_GAP,
    },
    askCard: {
      padding: space.lg,
      gap: space.md,
      borderColor: c.needsYouEdge,
      shadowColor: c.needsYou,
      shadowOpacity: 0.12,
      shadowRadius: 16,
      shadowOffset: { width: 0, height: 8 },
      elevation: 2,
    },
    askHeader: { flexDirection: "row", alignItems: "center", gap: space.sm },
    pill: {
      flexDirection: "row",
      alignItems: "center",
      gap: 6,
      height: 22,
      paddingHorizontal: 9,
      borderRadius: radius.pill,
      backgroundColor: c.needsYouWash,
    },
    pillDot: { width: 6, height: 6, borderRadius: 3 },
    pillLabel: { fontFamily: font.sansBold, fontSize: 11.5, color: c.needsYouText },
    askName: { flex: 1, fontFamily: font.mono, fontSize: size.caption, color: c.textSecondary },

    row: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.md,
      paddingVertical: 13,
      paddingHorizontal: 14,
    },
    rowNeedsYou: { borderColor: c.needsYouEdge },
    avatar: {
      width: 40,
      height: 40,
      borderRadius: radius.md,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.fill,
    },
    serversLine: { flexDirection: "row", alignItems: "center", gap: 5 },
    serversText: { fontFamily: font.mono, fontSize: 12, color: c.muted },
    avatarPulse: {
      position: "absolute",
      right: -8,
      bottom: -8,
    },
    rowBody: { flex: 1, gap: 3 },
    rowTop: { flexDirection: "row", alignItems: "baseline", gap: space.sm },
    name: {
      flex: 1,
      fontFamily: font.monoMedium,
      fontSize: 14.5,
      color: c.text,
    },
    age: { fontFamily: font.sans, fontSize: size.label, color: c.faint },
    meta: { fontFamily: font.mono, fontSize: 12, color: c.muted },
    metaWorking: { fontFamily: font.sansMedium, color: c.workingText },
    needsYouNote: {
      fontFamily: font.sansMedium,
      fontSize: size.caption,
      color: c.needsYouText,
      marginTop: space.xxs,
    },

    banner: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.md,
      marginTop: space.lg,
      padding: space.md,
      borderRadius: radius.xl,
      backgroundColor: c.errorWash,
      borderWidth: 1,
      borderColor: c.errorEdge,
    },
    bannerIcon: {
      width: 32,
      height: 32,
      borderRadius: radius.md,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.surface,
    },
    bannerCopy: { flex: 1 },
    bannerText: { fontFamily: font.sansBold, fontSize: size.caption, color: c.text },
    bannerHint: { fontFamily: font.sans, fontSize: size.caption, color: c.muted, marginTop: 2 },

    empty: {
      marginTop: space.xl,
      alignItems: "center",
      gap: space.sm,
    },
    emptyArt: {
      width: "100%",
      aspectRatio: 340 / 214,
      borderRadius: radius.sheet,
      overflow: "hidden",
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
      marginBottom: space.md,
    },
    emptyTitle: {
      fontFamily: font.sansBold,
      fontSize: 26,
      letterSpacing: -0.8,
      color: c.text,
      textAlign: "center",
    },
    emptyBody: {
      fontFamily: font.sans,
      fontSize: size.body,
      color: c.muted,
      lineHeight: 22,
      textAlign: "center",
      maxWidth: 320,
    },
    command: {
      alignSelf: "stretch",
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      marginTop: space.md,
      paddingHorizontal: space.lg,
      paddingVertical: 14,
      borderRadius: radius.lg,
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
    },
    commandPrompt: { fontFamily: font.mono, fontSize: 14, color: c.faint },
    commandText: { fontFamily: font.mono, fontSize: 14, color: c.text },
    emptyHint: { fontFamily: font.sans, fontSize: size.caption, color: c.faint, marginTop: space.xs },
    inlineMono: { fontFamily: font.mono, color: c.muted },
    secondaryButton: {
      marginTop: space.sm,
      minHeight: 44,
      paddingHorizontal: space.xl,
      borderRadius: radius.pill,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.fillStrong,
    },
    secondaryButtonText: { fontFamily: font.sansBold, fontSize: size.body, color: c.text },

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
      borderRadius: radius.pill,
      backgroundColor: c.inverse,
      shadowColor: c.shadow,
      shadowOpacity: 0.2,
      shadowRadius: 20,
      shadowOffset: { width: 0, height: 10 },
      elevation: 6,
      zIndex: 20,
    },
    undoText: { flex: 1, fontFamily: font.sans, fontSize: size.body, color: c.onInverse },
    undoAction: { fontFamily: font.sansBold, fontSize: size.body, color: c.inverseAccent },
    undoButton: { paddingVertical: space.xs, paddingHorizontal: space.sm },
  });
