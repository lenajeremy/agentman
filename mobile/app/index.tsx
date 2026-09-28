import Feather from "@expo/vector-icons/Feather";
import { Redirect, useRouter } from "expo-router";
import { useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  RefreshControl,
  ScrollView,
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
import { folderLabel } from "../lib/folders";
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

/** The states a chip can narrow the list to. */
type StateFilter = Session["state"];

export default function Agents() {
  const store = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [refreshing, setRefreshing] = useState(false);
  // Half a minute: fine enough that "now" turns into "1m" about when it should,
  // coarse enough to be free.
  useTicking(30_000);
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

  // Filtering happens here rather than in visibleSessions, so every chip
  // keeps showing its own count while another one is active. A chip whose
  // number vanished when you tapped its neighbour would be unreadable.
  const groups = useMemo(
    () => groupByState(store.visibleSessions, color, store.stateFilter),
    [store.visibleSessions, color, store.stateFilter],
  );
  const hiddenCount = store.sessions.length - store.visibleSessions.length;
  const incompatible = store.connection === "incompatible";

  if (!store.ready) return <View style={styles.page} />;
  if (!store.credentials) return <Redirect href="/pair" />;

  // Three tiles, and ended sessions are in none of them. The board answers
  // "does anything need me?", and a finished session never does — counting a
  // folder's forty of them as Idle would drown the one agent that is.
  const counts = store.visibleSessions.reduce(
    (current, session) => {
      if (session.state === "ended") {
        current.ended += 1;
        return current;
      }
      if (sessionNeedsAnswer(session)) current.needsYou += 1;
      else if (session.state === "busy") current.working += 1;
      else current.idle += 1;
      return current;
    },
    { needsYou: 0, working: 0, idle: 0, ended: 0 },
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
              <View style={styles.topActions}>
                <MotionPressable hitSlop={12} style={styles.iconButton} pressedScale={0.94}
                  onPress={() => router.push("/new-session")} accessibilityRole="button"
                  accessibilityLabel="New session" disabled={!store.daemonOnline}>
                  <Feather name="plus" size={22}
                    color={store.daemonOnline ? color.text : color.faint} />
                </MotionPressable>
                <MotionPressable hitSlop={12} style={styles.iconButton} pressedScale={0.94}
                  onPress={() => router.push("/settings")} accessibilityRole="button"
                  accessibilityLabel="Settings">
                  <Feather name="settings" size={19} color={color.text} />
                </MotionPressable>
              </View>
            </View>

            <Text style={styles.title} accessibilityRole="header">
              Agents
            </Text>

            {!store.daemonOnline ? (
              incompatible ? (
                <ProtocolBanner />
              ) : (
                <OfflineBanner lastSeenAt={store.lastSeenAt} />
              )
            ) : null}

            {/* One row of filters where there were three count tiles and a
                pill on separate lines.

                The tiles said "Working 1" directly above a section header
                saying "Working 1", so a third of the screen was spent
                repeating the list below it. Folding the counts into chips
                keeps the glance — the numbers are still the first thing you
                see — costs one row instead of three, and gives each number
                something to do: tapping one narrows the list to that state. */}
            <FilterBar counts={counts} ended={counts.ended} />

            {store.visibleSessions.length === 0 &&
              store.daemonOnline &&
              (hiddenCount > 0 ? (
                <AllHiddenState
                  count={hiddenCount}
                  onShow={store.restoreAllSessions}
                />
              ) : (
                <EmptyState folder={store.folderFilter} />
              ))}
          </ContentColumn>
        }
        renderSectionHeader={({ section }) => (
          <ContentColumn style={styles.gutter}>
            <View style={styles.groupHeading}>
              <Text style={[styles.groupLabel, { color: section.tint }]}>
                {section.label}
              </Text>
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
 * The one control the folder filter adds.
 *
 * Closed it reads "All folders" and the screen behaves exactly as it always
 * has. Chosen, it carries the folder in mono — it is a path, which is machine
 * text — and a separate button clears it, so the common way out is one tap
 * and not a trip back through the picker.
 */
function FolderFilter() {
  const store = useStore();
  const router = useRouter();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const chosen = store.folderFilter;

  return (
    <View style={styles.filterRow}>
      <MotionPressable
        onPress={() => router.push("/folders")}
        style={[styles.filter, chosen && styles.filterOn]}
        pressedScale={0.97}
        disabled={!store.daemonOnline}
        accessibilityRole="button"
        accessibilityLabel={
          chosen ? `Filtering by ${chosen}. Change folder` : "Filter by folder"
        }
      >
        <Feather
          name="folder"
          size={15}
          color={chosen ? color.workingText : color.muted}
        />
        <Text
          style={[styles.filterLabel, chosen && styles.filterLabelOn]}
          numberOfLines={1}
          ellipsizeMode="head"
        >
          {chosen ? folderLabel(chosen) : "All folders"}
        </Text>
        {store.folderLoading ? (
          <ActivityIndicator size="small" color={color.workingText} />
        ) : (
          <Feather
            name="chevron-down"
            size={16}
            color={chosen ? color.workingText : color.faint}
          />
        )}
      </MotionPressable>
      {chosen ? (
        <MotionPressable
          onPress={() => store.setFolderFilter(null)}
          style={styles.filterClear}
          pressedScale={0.94}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel="Clear folder filter"
        >
          <Feather name="x" size={16} color={color.muted} />
        </MotionPressable>
      ) : null}
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
function groupByState(
  sessions: Session[],
  color: Palette,
  only: StateFilter | null = null,
) {
  const buckets = new Map<
    string,
    { label: string; tint: string; rank: number; sessions: Session[] }
  >();
  for (const session of sessions) {
    const state = effectiveSessionState(session);
    if (only && state !== only) continue;
    const meta = stateStyle(state, color);
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
      sessions: bucket.sessions.sort(
        (a, b) => b.lastActivityAt - a.lastActivityAt,
      ),
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
            <View
              style={[styles.pillDot, { backgroundColor: color.needsYou }]}
            />
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
  const ended = displayState === "ended";
  return (
    <MotionPressable
      onPress={open}
      style={[styles.card, styles.row, needsYou && styles.rowNeedsYou, ended && styles.rowEnded]}
      pressedScale={0.985}
      accessibilityRole="button"
      accessibilityLabel={`${session.name}, ${stateStyle(displayState, color).label}`}
    >
      <View
        style={[
          styles.avatar,
          busy && { backgroundColor: color.workingWash },
          needsYou && { backgroundColor: color.needsYouWash },
          // Ended has no colour of its own, by the same rule idle has none:
          // nothing is happening, so nothing glows. What separates the two is
          // weight — an ended row recedes a step rather than lighting up.
          ended && styles.avatarEnded,
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
          <Text
            style={[styles.name, ended && styles.nameEnded]}
            numberOfLines={1}
          >
            {session.name}
          </Text>
          <Text style={styles.age}>{ago(session.lastActivityAt)}</Text>
        </View>
        {/* Model then path, matching the session header, so the same two facts
            read in the same order wherever you meet them.

            "Working ·" used to lead this line. The avatar beside it is already
            pulsing, which says the same thing faster and without spending
            characters the path then loses at the other end. */}
        <View style={styles.metaRow}>
          {/* Filtered to one folder, the path is the same on every row and
              says nothing. The agent's name takes the space instead, which
              is the fact that does vary once a folder holds five CLIs. */}
          {store.folderFilter ? (
            <Text style={styles.meta} numberOfLines={1}>
              {session.model ? `${agent.name} · ${session.model}` : agent.name}
            </Text>
          ) : (
            <>
              <Text style={styles.meta} numberOfLines={1}>
                {session.model ?? agent.name}
                {" · "}
              </Text>
              <Text
                style={[styles.meta, styles.metaPath]}
                numberOfLines={1}
                ellipsizeMode="head"
              >
                {shortPath(session.cwd)}
              </Text>
            </>
          )}
        </View>
        {session.servers?.length ? (
          <View style={styles.serversLine}>
            <Feather name="globe" size={11} color={color.ok} />
            <Text style={styles.serversText} numberOfLines={1}>
              {session.servers.map((server) => `:${server.port}`).join("  ")}
            </Text>
          </View>
        ) : null}
        {needsYou ? (
          <Text style={styles.needsYouNote}>Waiting on your answer</Text>
        ) : null}
      </View>
      <Feather name="chevron-right" size={18} color={color.faint} />
    </MotionPressable>
  );
}

/**
 * Re-renders on a timer, so "2m" becomes "3m" on its own.
 *
 * ago() reads the clock when it renders and nothing was making it render
 * again. A session the daemon has no news about would sit showing the age it
 * happened to have when you arrived, which is the one number on the row that
 * is supposed to be moving.
 */
function useTicking(everyMs: number) {
  const [, setTick] = useState(0);
  useEffect(() => {
    const timer = setInterval(() => setTick((value) => value + 1), everyMs);
    return () => clearInterval(timer);
  }, [everyMs]);
}

function effectiveSessionState(session: Session): Session["state"] {
  return sessionNeedsAnswer(session) ? "waiting_input" : session.state;
}

function ConnectionChip({
  online,
  incompatible,
}: {
  online: boolean;
  incompatible: boolean;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <View style={styles.chip} accessibilityRole="text">
      <View
        style={[
          styles.chipDot,
          { backgroundColor: online ? color.ok : color.error },
        ]}
      />
      <Text style={styles.chipText}>
        {online
          ? "Mac connected"
          : incompatible
            ? "Update required"
            : "Reconnecting"}
      </Text>
    </View>
  );
}

/**
 * The one row of controls above the list.
 *
 * Folder on the left, then a count per state. Horizontally scrollable rather
 * than wrapped: a second line here would put the first agent below the fold
 * again, which is the thing this replaced.
 */
function FilterBar({
  counts,
  ended,
}: {
  counts: { needsYou: number; working: number; idle: number };
  ended: number;
}) {
  const store = useStore();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const chips: { key: StateFilter; label: string; value: number; tint: string }[] = [
    { key: "waiting_input", label: "Needs you", value: counts.needsYou, tint: color.needsYouText },
    { key: "busy", label: "Working", value: counts.working, tint: color.working },
    { key: "idle", label: "Idle", value: counts.idle, tint: color.muted },
  ];
  // Ended only exists inside a folder, so its chip only does.
  if (ended > 0) {
    chips.push({ key: "ended", label: "Ended", value: ended, tint: color.muted });
  }

  return (
    <ScrollView
      horizontal
      showsHorizontalScrollIndicator={false}
      style={styles.filterBar}
      contentContainerStyle={styles.filterBarRow}
      keyboardShouldPersistTaps="handled"
    >
      <FolderFilter />
      {chips.map((chip) => {
        const on = store.stateFilter === chip.key;
        return (
          <MotionPressable
            key={chip.key}
            onPress={() => store.setStateFilter(on ? null : chip.key)}
            style={[styles.stateChip, on && styles.stateChipOn]}
            pressedScale={0.96}
            accessibilityRole="button"
            accessibilityState={{ selected: on }}
            accessibilityLabel={`${chip.value} ${chip.label}${on ? ", showing only these" : ""}`}
          >
            <Text style={[styles.stateChipValue, { color: on ? color.onInverse : chip.tint }]}>
              {chip.value}
            </Text>
            <Text style={[styles.stateChipLabel, on && styles.stateChipLabelOn]}>{chip.label}</Text>
          </MotionPressable>
        );
      })}
    </ScrollView>
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
          Your Mac is offline
          {lastSeenAt ? ` · last seen ${ago(lastSeenAt)} ago` : ""}
        </Text>
        <Text style={styles.bannerHint}>
          Agents appear here when it reconnects.
        </Text>
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

function EmptyState({ folder }: { folder: string | null }) {
  const styles = useStyles(makeStyles);
  // A filtered folder that turns up nothing is a different fact from having
  // no agents at all, and the advice for it is different too: the fix is to
  // widen the filter, not to open a terminal.
  if (folder) {
    return (
      <View style={styles.empty}>
        <EmptyIllustration style={styles.emptyArt} />
        <Text style={styles.emptyTitle}>Nothing here yet</Text>
        <Text style={styles.emptyBody}>
          No agent has run in {folderLabel(folder)}. Pick another folder, or
          clear the filter to see everything.
        </Text>
      </View>
    );
  }
  return (
    <View style={styles.empty}>
      <EmptyIllustration style={styles.emptyArt} />
      <Text style={styles.emptyTitle}>Nothing running</Text>
      <Text style={styles.emptyBody}>
        Tap + to start an agent in a folder on your Mac, or start one from its
        terminal. Cursor IDE sessions are view-only.
      </Text>
      <View style={styles.command}>
        <Text style={styles.commandPrompt}>$</Text>
        <Text style={styles.commandText} selectable>
          am claude
        </Text>
      </View>
      <Text style={styles.emptyHint}>
        Also <Text style={styles.inlineMono}>am codex</Text> and{" "}
        <Text style={styles.inlineMono}>am opencode</Text>. Recent Cursor IDE
        chats appear automatically in read-only mode.
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
    topActions: { flexDirection: "row", gap: space.sm },
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
    chipText: {
      fontFamily: font.sansMedium,
      fontSize: size.caption,
      color: c.textSecondary,
    },
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


    filterBar: { marginTop: space.lg, marginHorizontal: -space.lg },
    filterBarRow: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      paddingHorizontal: space.lg,
    },
    stateChip: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.xs,
      minHeight: 36,
      paddingHorizontal: space.md,
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      borderWidth: StyleSheet.hairlineWidth,
      borderColor: c.fillStrong,
    },
    stateChipOn: { backgroundColor: c.inverse, borderColor: c.inverse },
    stateChipValue: {
      fontFamily: font.sansBold,
      fontSize: size.caption,
      fontVariant: ["tabular-nums"],
    },
    stateChipLabel: {
      fontFamily: font.sans,
      fontSize: size.caption,
      color: c.muted,
    },
    stateChipLabelOn: { color: c.onInverse },
    rowEnded: { backgroundColor: c.paper },
    avatarEnded: { opacity: 0.55 },
    nameEnded: { color: c.textSecondary },
    // No top margin: the filter row that contains this owns the spacing now.
    filterRow: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
    },
    filter: {
      // Bounded rather than flexible: inside a horizontal scroller nothing
      // would shrink it, so a deep path would push every chip off-screen.
      maxWidth: 230,
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      minHeight: 40,
      paddingHorizontal: space.md,
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      borderWidth: StyleSheet.hairlineWidth,
      borderColor: c.fillStrong,
    },
    filterOn: { backgroundColor: c.workingWash, borderColor: c.workingSoft },
    filterLabel: {
      flexShrink: 1,
      fontFamily: font.sans,
      fontSize: size.body,
      color: c.textSecondary,
    },
    filterLabelOn: {
      fontFamily: font.monoMedium,
      fontSize: size.caption,
      color: c.workingText,
    },
    filterClear: {
      width: 40,
      height: 40,
      alignItems: "center",
      justifyContent: "center",
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      borderWidth: StyleSheet.hairlineWidth,
      borderColor: c.fillStrong,
    },
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
    pillLabel: {
      fontFamily: font.sansBold,
      fontSize: 11.5,
      color: c.needsYouText,
    },
    askName: {
      flex: 1,
      fontFamily: font.mono,
      fontSize: size.caption,
      color: c.textSecondary,
    },

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
    metaRow: { flexDirection: "row", alignItems: "center" },
    // The model holds its width and the path gives way from its front: a
    // path's last segment is the one that says which project this is.
    metaPath: { flexShrink: 1 },
    meta: { fontFamily: font.mono, fontSize: 12, color: c.muted },
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
    bannerText: {
      fontFamily: font.sansBold,
      fontSize: size.caption,
      color: c.text,
    },
    bannerHint: {
      fontFamily: font.sans,
      fontSize: size.caption,
      color: c.muted,
      marginTop: 2,
    },

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
    emptyHint: {
      fontFamily: font.sans,
      fontSize: size.caption,
      color: c.faint,
      marginTop: space.xs,
    },
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
    secondaryButtonText: {
      fontFamily: font.sansBold,
      fontSize: size.body,
      color: c.text,
    },

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
    undoText: {
      flex: 1,
      fontFamily: font.sans,
      fontSize: size.body,
      color: c.onInverse,
    },
    undoAction: {
      fontFamily: font.sansBold,
      fontSize: size.body,
      color: c.inverseAccent,
    },
    undoButton: { paddingVertical: space.xs, paddingHorizontal: space.sm },
  });
