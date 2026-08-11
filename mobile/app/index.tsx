import { Redirect, useRouter } from "expo-router";
import { useEffect, useMemo, useState } from "react";
import {
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { Pulse } from "../components/Pulse";
import { QuestionCard } from "../components/QuestionCard";
import { SwipeToDismiss } from "../components/SwipeToDismiss";
import { canDismiss } from "../lib/dismissed";
import { Session } from "../lib/protocol";
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

  if (!store.ready) return <View style={styles.page} />;
  if (!store.credentials) return <Redirect href="/pair" />;

  const working = store.visibleSessions.filter((s) => s.state === "busy").length;

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      <View style={styles.header}>
        <View>
          <Text style={styles.wordmark}>agentman</Text>
          <Text style={styles.subhead}>
            {store.visibleSessions.length === 0
              ? "No agents running"
              : `${store.visibleSessions.length} agent${store.visibleSessions.length === 1 ? "" : "s"}` +
                (working > 0 ? ` · ${working} working` : "")}
          </Text>
        </View>
        <Pressable
          hitSlop={12}
          style={styles.gear}
          onPress={() => router.push("/settings")}
          accessibilityRole="button"
          accessibilityLabel="Settings"
        >
          <Text style={styles.gearGlyph}>⋯</Text>
        </Pressable>
      </View>

      {!store.daemonOnline && <OfflineBanner lastSeenAt={store.lastSeenAt} />}

      <ScrollView
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
      >
        {store.visibleSessions.length === 0 && store.daemonOnline && (
          hiddenCount > 0 ? (
            <AllHiddenState count={hiddenCount} onShow={store.restoreAllSessions} />
          ) : (
            <EmptyState />
          )
        )}

        {groups.map(({ label, tint, sessions }) => (
          <View key={label} style={styles.group}>
            <Text style={[styles.groupLabel, { color: tint }]}>{label}</Text>
            {sessions.map((session) => (
              <SwipeToDismiss
                key={session.id}
                enabled={canDismiss(session)}
                onDismiss={() => {
                  store.dismissSession(session.id);
                  setUndo({ id: session.id, name: session.name });
                }}
                accessibilityLabel={`${session.name}, ${stateStyle(session.state).label}`}
              >
                <AgentRow session={session} />
              </SwipeToDismiss>
            ))}
          </View>
        ))}
      </ScrollView>

      {undo && (
        <View style={[styles.undoBar, { bottom: insets.bottom + space.lg }]}>
          <Text style={styles.undoText} numberOfLines={1}>
            Hid {undo.name}
          </Text>
          <Pressable
            hitSlop={10}
            onPress={() => {
              store.restoreSession(undo.id);
              setUndo(null);
            }}
            accessibilityRole="button"
          >
            <Text style={styles.undoAction}>Undo</Text>
          </Pressable>
        </View>
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
  const buckets = new Map<string, { label: string; tint: string; rank: number; sessions: Session[] }>();
  for (const session of sessions) {
    const meta = stateStyle(session.state);
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
  const needsYou = session.state === "waiting_input";

  return (
    <Pressable
      onPress={() => router.push(`/session/${encodeURIComponent(session.id)}`)}
      style={({ pressed }) => [
        styles.row,
        needsYou && styles.rowNeedsYou,
        pressed && styles.rowPressed,
      ]}
      accessibilityRole="button"
      accessibilityLabel={`${session.name}, ${stateStyle(session.state).label}`}
    >
      <Pulse state={session.state} />
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
          <Text style={styles.model} numberOfLines={1}>
            {session.model ?? session.kind}
          </Text>
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
    </Pressable>
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
      <Pressable hitSlop={10} onPress={onShow} accessibilityRole="button">
        <Text style={styles.emptyAction}>Show them anyway</Text>
      </Pressable>
    </View>
  );
}

function OfflineBanner({ lastSeenAt }: { lastSeenAt: number | null }) {
  return (
    <View style={styles.banner}>
      <Text style={styles.bannerText}>
        Your Mac is offline{lastSeenAt ? ` · last seen ${ago(lastSeenAt)} ago` : ""}
      </Text>
      <Text style={styles.bannerHint}>Agents appear here when it reconnects.</Text>
    </View>
  );
}

function EmptyState() {
  return (
    <View style={styles.empty}>
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

  header: {
    flexDirection: "row",
    alignItems: "flex-start",
    justifyContent: "space-between",
    paddingHorizontal: space.lg,
    paddingTop: space.lg,
    paddingBottom: space.md,
  },
  wordmark: {
    fontFamily: font.mono,
    fontSize: size.display,
    color: color.text,
    letterSpacing: -0.5,
  },
  subhead: {
    fontFamily: font.sans,
    fontSize: size.caption,
    color: color.muted,
    marginTop: 2,
  },
  gear: { padding: space.xs },
  gearGlyph: { color: color.muted, fontSize: 22, lineHeight: 24 },

  group: { marginTop: space.lg },
  groupLabel: {
    fontFamily: font.sansMedium,
    fontSize: size.label,
    letterSpacing: 1.4,
    textTransform: "uppercase",
    paddingHorizontal: space.lg,
    marginBottom: space.sm,
  },

  row: {
    flexDirection: "row",
    alignItems: "flex-start",
    gap: space.sm,
    paddingVertical: space.md,
    paddingHorizontal: space.md,
    marginHorizontal: space.md,
    borderRadius: radius.md,
    backgroundColor: color.surface,
    marginBottom: space.sm,
    borderLeftWidth: 2,
    borderLeftColor: "transparent",
  },
  // The one place the design raises its voice: an agent that cannot continue
  // without the user.
  rowNeedsYou: { borderLeftColor: color.needsYou, backgroundColor: "#1B1A17" },
  rowPressed: { backgroundColor: color.sunken },

  rowBody: { flex: 1, gap: 2 },
  rowTop: { flexDirection: "row", alignItems: "baseline", gap: space.sm },
  name: {
    flex: 1,
    fontFamily: font.monoMedium,
    fontSize: size.body,
    color: color.text,
  },
  age: { fontFamily: font.sans, fontSize: size.caption, color: color.faint },
  rowMeta: { flexDirection: "row", alignItems: "baseline", gap: space.sm },
  path: { flex: 1, fontFamily: font.mono, fontSize: size.caption, color: color.muted },
  // The model, or the agent kind until the agent has replied once and named
  // one. Falling back to the kind rather than showing nothing keeps the column
  // from flickering into existence on the first reply.
  model: { fontFamily: font.mono, fontSize: size.caption, color: color.faint },
  needsYouNote: {
    fontFamily: font.sansMedium,
    fontSize: size.caption,
    color: color.needsYou,
    marginTop: space.xs,
  },

  banner: {
    marginHorizontal: space.md,
    padding: space.md,
    borderRadius: radius.md,
    backgroundColor: color.surface,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.line,
  },
  bannerText: { fontFamily: font.sansMedium, fontSize: size.caption, color: color.text },
  bannerHint: { fontFamily: font.sans, fontSize: size.caption, color: color.muted, marginTop: 2 },

  empty: { paddingHorizontal: space.xl, paddingTop: space.xxl, gap: space.sm },
  emptyTitle: { fontFamily: font.sansBold, fontSize: size.title, color: color.text },
  emptyBody: { fontFamily: font.sans, fontSize: size.body, color: color.muted, lineHeight: 22 },
  code: { fontFamily: font.mono, color: color.working },
  emptyAction: {
    fontFamily: font.sansMedium,
    fontSize: size.body,
    color: color.working,
    marginTop: space.sm,
  },

  undoBar: {
    position: "absolute",
    left: space.lg,
    right: space.lg,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: space.md,
    paddingVertical: space.md,
    paddingHorizontal: space.lg,
    borderRadius: radius.md,
    backgroundColor: color.surface,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: color.line,
  },
  undoText: { flex: 1, fontFamily: font.sans, fontSize: size.body, color: color.muted },
  undoAction: { fontFamily: font.sansMedium, fontSize: size.body, color: color.working },
});
