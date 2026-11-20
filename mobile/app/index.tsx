import Feather from "@expo/vector-icons/Feather";
import * as Clipboard from "expo-clipboard";
import * as Haptics from "expo-haptics";
import { Redirect, useFocusEffect, useLocalSearchParams, useRouter } from "expo-router";
import { type ComponentRef, useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  Platform,
  RefreshControl,
  ScrollView,
  SectionList,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { Appear } from "../components/Appear";
import { AgentIcon } from "../components/AgentIcon";
import { AgentmanCard } from "../components/AgentmanCard";
import { Loader } from "../components/Loader";
import { ContentColumn } from "../components/ContentColumn";
import { StickerOnDots, edged, eyebrow, hardShadow } from "../components/Brand";
import { EmptyIllustration } from "../components/Illustrations";
import { MotionPressable } from "../components/MotionPressable";
import { Popover, type PopoverItem } from "../components/Popover";
import { Pulse } from "../components/Pulse";
import { QuestionCard } from "../components/QuestionCard";
import { ROW_GAP, SwipeToDismiss } from "../components/SwipeToDismiss";
import { useStyles, useTheme } from "../lib/appearance";
import { canDismiss } from "../lib/dismissed";
import { emptyBoard, folderContains, folderLabel } from "../lib/folders";
import { Session, type SearchHit } from "../lib/protocol";
import { markedSegments, markWords, searchable } from "../lib/search";
import { outOfUsageLabel } from "../lib/out-of-usage";
import { sessionNeedsAnswer } from "../lib/question-alerts";
import { draftNamespace } from "../lib/draft-policy";
import { hostName, useFleet, type Host } from "../lib/fleet";
import { useStore, type Store } from "../lib/store";
import { useUpdates } from "../lib/updates-context";
import {
  agentLabel,
  ago,
  font,
  Palette,
  radius,
  size,
  space,
  stateStyle,
  scaled,
} from "../lib/theme";
import { folderName, modelLabel } from "../lib/session-view";

/** The states a chip can narrow the list to. */
type StateFilter = Session["state"];

type NamedHost = { host: Host; name: string };
type FleetSession = {
  /** Composite key so duplicate session IDs on different computers never collide. */
  key: string;
  host: Host;
  deviceName: string;
  session: Session;
};
type FleetSearchHit = {
  key: string;
  host: Host;
  deviceName: string;
  hit: SearchHit;
};
type FleetSearchResults = {
  query: string;
  hits: FleetSearchHit[];
  indexing: boolean;
};

function fleetSessionKey(hostId: string, sessionId: string): string {
  return `${hostId}\0${sessionId}`;
}

/** Set once an update has been offered, until the app is next launched. */
let promptedThisLaunch = false;

export default function Agents() {
  const fleet = useFleet();
  const activeStore = useStore();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [refreshing, setRefreshing] = useState(false);
  // Half a minute: fine enough that "now" turns into "1m" about when it should,
  // coarse enough to be free.
  useTicking(30_000);
  /** The row just swiped away, offered back for a few seconds. */
  const [undo, setUndo] = useState<{ key: string; hostId: string; id: string; name: string } | null>(null);
  /** State chip selection while viewing all paired devices together. */
  const [fleetStateFilter, setFleetStateFilter] = useState<StateFilter | null>(null);
  /** What is typed in the search box, and what the paired Macs found for it. A
   *  link to the list can carry a search to start with, as ?q=. */
  const { q } = useLocalSearchParams<{ q?: string }>();
  const [query, setQuery] = useState(typeof q === "string" ? q : "");
  const [found, setFound] = useState<FleetSearchResults | null>(null);
  const [searchProblem, setSearchProblem] = useState<string | null>(null);
  const trimmed = query.trim();
  const searching = trimmed.length > 0;

  const namedHosts = useMemo<NamedHost[]>(
    () => fleet.hosts.map((host, index) => ({ host, name: hostName(host, index) })),
    [fleet.hosts],
  );
  const multiDevice = namedHosts.length > 1;
  const filteredHost = useMemo(
    () => (fleet.filter ? namedHosts.find((entry) => entry.host.id === fleet.filter) ?? null : null),
    [fleet.filter, namedHosts],
  );
  const shownHosts = useMemo(
    () => (filteredHost ? [filteredHost] : namedHosts),
    [filteredHost, namedHosts],
  );
  const showingAllDevices = multiDevice && !filteredHost;
  const focusedHost = filteredHost ?? (namedHosts.length === 1 ? namedHosts[0] : null);
  const focusedStore = focusedHost?.host.store ?? activeStore;
  const activeStateFilter = showingAllDevices ? fleetStateFilter : focusedStore.stateFilter;
  const setActiveStateFilter = useCallback(
    (next: StateFilter | null) => {
      if (showingAllDevices) setFleetStateFilter(next);
      else focusedStore.setStateFilter(next);
    },
    [showingAllDevices, focusedStore],
  );

  const rows = useMemo<FleetSession[]>(
    () =>
      shownHosts.flatMap(({ host, name }) => {
        const list = showingAllDevices ? host.store.liveSessions : host.store.visibleSessions;
        return list.map((session) => ({
          key: fleetSessionKey(host.id, session.id),
          host,
          deviceName: name,
          session,
        }));
      }),
    [shownHosts, showingAllDevices],
  );

  const shownIds = shownHosts.map(({ host }) => host.id).join("\0");

  // What was said, a moment after typing stops: over the relay a search per
  // key would queue up behind itself.
  useEffect(() => {
    setSearchProblem(null);
    if (!searchable(trimmed)) {
      setFound(null);
      return;
    }
    let current = true;
    const timer = setTimeout(() => {
      const targets = shownHosts.filter(({ host }) => host.store.daemonOnline);
      if (targets.length === 0) {
        if (current) setFound({ query: trimmed, hits: [], indexing: false });
        return;
      }
      void Promise.allSettled(
        targets.map(async ({ host, name }) => ({
          host,
          deviceName: name,
          results: await host.store.search(trimmed),
        })),
      ).then((settled) => {
        if (!current) return;
        const hits: FleetSearchHit[] = [];
        let indexing = false;
        let firstError: string | null = null;
        let succeeded = 0;
        for (const outcome of settled) {
          if (outcome.status === "fulfilled") {
            succeeded += 1;
            if (outcome.value.results.indexing) indexing = true;
            for (const hit of outcome.value.results.hits) {
              if (!hit.session) continue;
              hits.push({
                key: fleetSessionKey(outcome.value.host.id, hit.session.id),
                host: outcome.value.host,
                deviceName: outcome.value.deviceName,
                hit,
              });
            }
          } else if (!firstError) {
            firstError =
              outcome.reason instanceof Error ? outcome.reason.message : "Search failed.";
          }
        }
        if (succeeded === 0 && firstError) {
          setSearchProblem(firstError);
          return;
        }
        setFound({ query: trimmed, hits, indexing });
      });
    }, 300);
    return () => {
      current = false;
      clearTimeout(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [trimmed, shownIds]);
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

  const activeFolderFilter = showingAllDevices ? null : focusedStore.folderFilter;
  const activeFolderRoot = showingAllDevices ? null : focusedStore.folderRoot;

  // Filtering happens here rather than in visibleSessions, so every chip
  // keeps showing its own count while another one is active. A chip whose
  // number vanished when you tapped its neighbour would be unreadable.
  const groups = useMemo(
    () => groupByState(rows, color, activeStateFilter),
    [rows, color, activeStateFilter],
  );
  const results = useMemo(
    () =>
      searching
        ? searchSections(
            rows,
            trimmed,
            found,
            color,
            activeFolderFilter
              ? (cwd: string) => folderContains(activeFolderFilter, activeFolderRoot, cwd)
              : null,
            showingAllDevices,
          )
        : null,
    [searching, rows, trimmed, found, color, activeFolderFilter, activeFolderRoot, showingAllDevices],
  );

  const hiddenCount = shownHosts.reduce((sum, { host }) => {
    const shown = showingAllDevices ? host.store.liveSessions.length : host.store.visibleSessions.length;
    return sum + Math.max(0, host.store.sessions.length - shown);
  }, 0);

  const onlineCount = shownHosts.filter(({ host }) => host.store.daemonOnline).length;
  const anyOnline = onlineCount > 0;
  const anyIncompatible = shownHosts.some(({ host }) => host.store.connection === "incompatible");
  const latestSeenAt = shownHosts.reduce<number | null>((latest, { host }) => {
    const seen = host.store.lastSeenAt;
    if (!seen) return latest;
    return latest === null || seen > latest ? seen : latest;
  }, null);

  // Nothing has run on these machines yet: the empty text says to tap +, so the
  // + turns the site's orange until there is an agent.
  const nothingRunning =
    color.look === "site" &&
    rows.length === 0 &&
    anyOnline &&
    !activeFolderFilter &&
    emptyBoard(activeFolderFilter, hiddenCount) !== "hidden";

  // A newer app or Mac is offered here, on the board, once per version: never
  // over a session someone is reading, not before the board has drawn, and
  // not twice in one launch, so the two never arrive back to back.
  const { pending } = useUpdates();
  const paired = namedHosts.length > 0 || Boolean(activeStore.credentials);
  useFocusEffect(
    useCallback(() => {
      if (!pending || !paired || promptedThisLaunch) return;
      const timer = setTimeout(() => {
        promptedThisLaunch = true;
        router.push(`/update?kind=${pending}`);
      }, 1200);
      return () => clearTimeout(timer);
    }, [pending, paired, router]),
  );

  if (!activeStore.ready) return <View style={styles.page} />;
  if (namedHosts.length === 0 && !activeStore.credentials) return <Redirect href="/pair" />;

  // Three tiles, and ended sessions are in none of them. The board answers
  // "does anything need me?", and a finished session never does — counting a
  // folder's forty of them as Idle would drown the one agent that is.
  const counts = rows.reduce(
    (current, { session }) => {
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

  const connectionLabel = !multiDevice
    ? anyOnline
      ? "Mac connected"
      : anyIncompatible
        ? "Update required"
        : "Reconnecting"
    : filteredHost
      ? filteredHost.host.store.daemonOnline
        ? filteredHost.name
        : filteredHost.host.store.connection === "incompatible"
          ? "Update required"
          : `${filteredHost.name} · Offline`
      : onlineCount === shownHosts.length
        ? `${onlineCount} devices online`
        : onlineCount > 0
          ? `${onlineCount} of ${shownHosts.length} online`
          : anyIncompatible
            ? "Update required"
            : "Offline";

  const offlineSubject = showingAllDevices
    ? "Your devices are"
    : filteredHost
      ? `${filteredHost.name} is`
      : "Your Mac is";

  const managerHosts = shownHosts.filter(({ host }) => host.manager.supported);

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      <SectionList
        style={styles.scroll}
        sections={(results?.sections ?? groups).map((group) => ({
          ...group,
          data: group.sessions,
        }))}
        keyExtractor={(item) => item.key}
        stickySectionHeadersEnabled={false}
        initialNumToRender={12}
        windowSize={7}
        contentContainerStyle={{ paddingBottom: insets.bottom + space.xxl }}
        keyboardShouldPersistTaps="handled"
        keyboardDismissMode="on-drag"
        refreshControl={
          <RefreshControl
            refreshing={refreshing}
            tintColor={color.faint}
            onRefresh={() => {
              setRefreshing(true);
              for (const { host } of shownHosts) host.store.refresh();
              setTimeout(() => setRefreshing(false), 600);
            }}
          />
        }
        ListHeaderComponent={
          <ContentColumn style={styles.gutter}>
            <View style={styles.topBar}>
              <ConnectionChip
                label={connectionLabel}
                online={anyOnline}
              />
              <View style={styles.topActions}>
                <MotionPressable
                  hitSlop={12}
                  style={[styles.iconButton, nothingRunning && styles.iconButtonFlood]}
                  pressedScale={0.94}
                  onPress={() => {
                    const target =
                      filteredHost?.host ??
                      shownHosts.find(({ host }) => host.id === fleet.active && host.store.daemonOnline)?.host ??
                      shownHosts.find(({ host }) => host.store.daemonOnline)?.host;
                    if (target) fleet.select(target.id);
                    router.push("/new-session");
                  }}
                  accessibilityRole="button"
                  accessibilityLabel="New session"
                  disabled={!anyOnline}
                >
                  <Feather
                    name="plus"
                    size={22}
                    color={nothingRunning ? color.edge : anyOnline ? color.text : color.faint}
                  />
                </MotionPressable>
                <MotionPressable
                  hitSlop={12}
                  style={styles.iconButton}
                  pressedScale={0.94}
                  onPress={() => router.push("/settings")}
                  accessibilityRole="button"
                  accessibilityLabel="Settings"
                >
                  <Feather name="settings" size={19} color={color.text} />
                </MotionPressable>
              </View>
            </View>

            <Text style={styles.title} accessibilityRole="header">
              Agents
            </Text>

            <SearchField value={query} onChange={setQuery} />

            {searching ? (
              <SearchStatus
                problem={searchProblem}
                pending={searchable(trimmed) && found?.query !== trimmed && !searchProblem}
                indexing={found?.indexing === true}
                empty={(results?.sections.length ?? 0) === 0}
              />
            ) : (
            <>
            {managerHosts.map(({ host, name }, idx) => (
              <AgentmanCard
                key={host.id}
                host={host}
                deviceName={showingAllDevices ? name : undefined}
                stacked={idx < managerHosts.length - 1}
                onSelect={() => fleet.select(host.id)}
              />
            ))}

            {anyIncompatible ? (
              <ProtocolBanner />
            ) : !anyOnline ? (
              <OfflineBanner lastSeenAt={latestSeenAt} subject={offlineSubject} />
            ) : null}

            {/* One row of filters where there were three count tiles and a
                pill on separate lines.

                The tiles said "Working 1" directly above a section header
                saying "Working 1", so a third of the screen was spent
                repeating the list below it. Folding the counts into chips
                keeps the glance — the numbers are still the first thing you
                see — costs one row instead of three, and gives each number
                something to do: tapping one narrows the list to that state. */}
            <FilterBar
              counts={counts}
              ended={counts.ended}
              stateFilter={activeStateFilter}
              setStateFilter={setActiveStateFilter}
              namedHosts={namedHosts}
              filteredHost={filteredHost}
              anyOnline={anyOnline}
            />

            {rows.length === 0 &&
              anyOnline &&
              (emptyBoard(activeFolderFilter, hiddenCount) === "hidden" ? (
                <AllHiddenState
                  count={hiddenCount}
                  onShow={() => {
                    for (const { host } of shownHosts) host.store.restoreAllSessions();
                  }}
                />
              ) : (
                <EmptyState folder={activeFolderFilter} />
              ))}
            </>
            )}
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
        renderItem={({ item, index, section }) => (
          <ContentColumn style={styles.gutter}>
            <SwipeToDismiss
              enabled={!searching && canDismiss(item.session)}
              onDismiss={() => {
                item.host.store.dismissSession(item.session.id);
                setUndo({
                  key: item.key,
                  hostId: item.host.id,
                  id: item.session.id,
                  name: item.session.name,
                });
              }}
              accessibilityLabel={`${item.session.name}, ${stateStyle(effectiveSessionState(item.session), color).label}`}
            >
              <AgentRow
                session={item.session}
                owner={item.host.store}
                selectOwner={() => fleet.select(item.host.id)}
                deviceName={showingAllDevices ? item.deviceName : undefined}
                first={index === 0}
                last={index === section.data.length - 1}
                match={results?.hits.get(item.key)}
                query={found?.query}
              />
            </SwipeToDismiss>
          </ContentColumn>
        )}
      />

      {undo && (
        <Appear
          key={undo.key}
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
              const target = fleet.hosts.find((host) => host.id === undo.hostId);
              target?.store.restoreSession(undo.id);
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
 * Device filter beside Folder and state chips: stays out of the way when only
 * one computer is paired, and opens a compact popover when two or more are.
 */
function DeviceFilter({
  namedHosts,
  filteredHost,
}: {
  namedHosts: NamedHost[];
  filteredHost: NamedHost | null;
}) {
  const fleet = useFleet();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [open, setOpen] = useState(false);
  const [menuTop, setMenuTop] = useState(210);
  const anchorRef = useRef<ComponentRef<typeof View>>(null);

  if (namedHosts.length < 2) return null;

  const onlineTotal = namedHosts.filter(({ host }) => host.store.daemonOnline).length;
  const openMenu = () => {
    const node = anchorRef.current;
    if (node?.measureInWindow) {
      node.measureInWindow((_x: number, y: number, _w: number, h: number) => {
        if (h > 0) setMenuTop(Math.max(space.md, y + h + space.xs - insets.top));
        setOpen(true);
      });
    } else {
      setOpen(true);
    }
  };

  const items: PopoverItem[] = [
    {
      key: "all",
      label: "All devices",
      icon: "layers",
      checked: !filteredHost,
      detail:
        onlineTotal === namedHosts.length
          ? `${onlineTotal} online`
          : `${onlineTotal}/${namedHosts.length} online`,
      onPress: () => {
        fleet.setFilter(null);
        setOpen(false);
      },
    },
    ...namedHosts.map(({ host, name }) => {
      const count = host.store.liveSessions.length;
      return {
        key: host.id,
        label: name,
        icon: "monitor" as const,
        checked: filteredHost?.host.id === host.id,
        detail: host.store.daemonOnline
          ? count === 1
            ? "1 agent"
            : `${count} agents`
          : "Offline",
        onPress: () => {
          fleet.select(host.id);
          fleet.setFilter(host.id);
          setOpen(false);
        },
      };
    }),
    {
      key: "manage",
      label: "Manage devices",
      icon: "sliders",
      onPress: () => {
        setOpen(false);
        router.push("/devices");
      },
    },
  ];

  return (
    <View ref={anchorRef} collapsable={false} style={styles.filterRow}>
      <MotionPressable
        onPress={openMenu}
        style={[styles.filter, filteredHost && styles.filterOn]}
        pressedScale={0.97}
        accessibilityRole="button"
        accessibilityLabel={
          filteredHost
            ? `Filtering by device ${filteredHost.name}. Change device`
            : "Filter by device"
        }
      >
        <Feather
          name="monitor"
          size={15}
          color={filteredHost ? color.workingText : color.muted}
        />
        <Text
          style={[styles.filterLabel, filteredHost && styles.deviceFilterLabelOn]}
          numberOfLines={1}
        >
          {filteredHost ? filteredHost.name : "All devices"}
        </Text>
        <Feather
          name="chevron-down"
          size={16}
          color={filteredHost ? color.workingText : color.faint}
        />
      </MotionPressable>
      {filteredHost ? (
        <MotionPressable
          onPress={() => fleet.setFilter(null)}
          style={styles.filterClear}
          pressedScale={0.94}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel="Clear device filter"
        >
          <Feather name="x" size={16} color={color.muted} />
        </MotionPressable>
      ) : null}
      <Popover
        visible={open}
        onDismiss={() => setOpen(false)}
        top={menuTop}
        align="left"
        title="Show agents from"
        items={items}
      />
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
function FolderFilter({
  host,
  anyOnline,
}: {
  host: NamedHost | null;
  anyOnline: boolean;
}) {
  const activeStore = useStore();
  const router = useRouter();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const store = host?.host.store ?? activeStore;
  const chosen = host ? store.folderFilter : null;
  const online = host ? store.daemonOnline : anyOnline;

  return (
    <View style={styles.filterRow}>
      <MotionPressable
        onPress={() => router.push("/folders")}
        style={[styles.filter, chosen && styles.filterOn]}
        pressedScale={0.97}
        disabled={!online}
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
        {host && store.folderLoading ? (
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
  rows: FleetSession[],
  color: Palette,
  only: StateFilter | null = null,
) {
  const buckets = new Map<
    string,
    { label: string; tint: string; rank: number; sessions: FleetSession[] }
  >();
  for (const row of rows) {
    const state = effectiveSessionState(row.session);
    if (only && state !== only) continue;
    const meta = stateStyle(state, color);
    const bucket = buckets.get(meta.label) ?? {
      label: meta.label,
      tint: meta.rank === 0 ? meta.text : color.muted,
      rank: meta.rank,
      sessions: [],
    };
    bucket.sessions.push(row);
    buckets.set(meta.label, bucket);
  }
  return Array.from(buckets.values())
    .map((bucket) => ({
      ...bucket,
      sessions: bucket.sessions.sort(
        (a, b) => b.session.lastActivityAt - a.session.lastActivityAt,
      ),
    }))
    .sort((a, b) => a.rank - b.rank);
}

function AgentRow({
  session,
  owner,
  selectOwner,
  deviceName,
  first = true,
  last = true,
  match,
  query,
}: {
  session: Session;
  owner?: Store;
  selectOwner?(): void;
  /** Shown on the row when sessions from more than one computer share the list. */
  deviceName?: string;
  /** Where the row sits in its section: a section reads as one card. */
  first?: boolean;
  last?: boolean;
  /** What a search found in its conversation, which a tap opens at. */
  match?: SearchHit;
  query?: string;
}) {
  const activeStore = useStore();
  const store = owner ?? activeStore;
  const router = useRouter();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const needsYou = sessionNeedsAnswer(session);
  const displayState = effectiveSessionState(session);
  const agent = agentLabel(session.kind);
  const open = () => {
    selectOwner?.();
    if (!store.credentials) return;
    if (match && !store.sessions.some(other => other.id === session.id)) store.rememberSession(session);
    router.push({
      pathname: "/session/[id]",
      params: {
        id: session.id, device: draftNamespace(store.credentials),
        ...(match ? { message: match.messageId, find: query ?? "" } : {}),
      },
    });
  };

  if (session.question && !match) {
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
          accessibilityLabel={`${session.name}${deviceName ? ` on ${deviceName}` : ""}, needs you. Open session`}
        >
          <View style={styles.pill}>
            <View
              style={[styles.pillDot, { backgroundColor: color.needsYou }]}
            />
            <Text style={styles.pillLabel}>Needs you</Text>
          </View>
          <Text style={styles.askName} numberOfLines={1}>
            {deviceName ? `${session.name} · ${deviceName}` : session.name}
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
  // Stopped until its plan resets: said under its name, and marked on it.
  const outOfUsage = !busy && !ended ? session.limit : undefined;
  return (
    <MotionPressable
      onPress={open}
      style={[
        styles.groupRow,
        first && styles.groupFirst,
        last ? styles.groupLast : styles.groupDivider,
        styles.row,
        needsYou && styles.rowNeedsYou,
        ended && styles.rowEnded,
      ]}
      pressedScale={0.985}
      accessibilityRole="button"
      accessibilityLabel={`${session.name}${deviceName ? ` on ${deviceName}` : ""}, ${outOfUsage ? outOfUsageLabel(outOfUsage) : stateStyle(displayState, color).label}`}
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
        <AgentIcon kind={session.kind} size={20} />
        {displayState === "busy" ? (
          // The Mac's working ring, on a disc cut out of the avatar's corner.
          <View style={styles.avatarLoader}>
            <Loader size={12} />
          </View>
        ) : displayState !== "idle" && displayState !== "ended" ? (
          <View style={styles.avatarPulse}>
            <Pulse state={displayState} size={7} />
          </View>
        ) : outOfUsage ? (
          <View style={styles.avatarLoader}>
            <Feather name="slash" size={10} color={color.error} />
          </View>
        ) : null}
      </View>
      <View style={styles.rowBody}>
        <View style={styles.rowTop}>
          {/* A name people read, and often gave: set like the rest of what a
              person reads, not like a path. */}
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
          {!deviceName && store.folderFilter ? (
            <Text style={styles.meta} numberOfLines={1}>
              {session.model ? `${agent.name} · ${modelLabel(session.model)}` : agent.name}
            </Text>
          ) : (
            <Text style={styles.meta} numberOfLines={1}>
              {session.model ? modelLabel(session.model) : agent.name}
              {" · "}
              {/* The project, by its folder's name: the part of a path that
                  says which one this is. */}
              {folderName(session.cwd)}
            </Text>
          )}
        </View>
        {deviceName ? (
          <View style={styles.deviceLine}>
            <Feather name="monitor" size={11} color={color.faint} />
            <Text style={styles.deviceText} numberOfLines={1}>
              {deviceName}
            </Text>
          </View>
        ) : null}
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
        {outOfUsage ? (
          <View style={styles.limitLine}>
            <Feather name="slash" size={11} color={color.error} />
            <Text style={styles.limitNote} numberOfLines={1}>
              {outOfUsageLabel(outOfUsage)}
            </Text>
          </View>
        ) : null}
        {match ? <SnippetLine hit={match} query={query ?? ""} /> : null}
      </View>
      <Feather name="chevron-right" size={18} color={color.faint} />
    </MotionPressable>
  );
}

/** What was said that matched: who said it, then the line, its words in
 *  the text colour and bold, as on the Mac and in T3 Code. */
function SnippetLine({ hit, query }: { hit: SearchHit; query: string }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const user = hit.role === "user";
  return (
    <Text style={styles.snippet} numberOfLines={2}>
      <Text style={{ color: user ? color.workingText : color.ok }}>{user ? "You: " : "Agent: "}</Text>
      {markedSegments(hit.snippet, markWords(query)).map((segment, index) =>
        segment.match ? (
          <Text key={index} style={styles.snippetMatch}>
            {segment.text}
          </Text>
        ) : (
          segment.text
        ),
      )}
    </Text>
  );
}

/**
 * Searching, the list is what matched: agents whose names or folders have
 * the words, then the conversations found only by what was said in them,
 * running or long over. A conversation is listed once, under its name when
 * that matched, with what was said under it either way.
 */
function searchSections(
  rows: FleetSession[],
  query: string,
  found: FleetSearchResults | null,
  color: Palette,
  /** The folder filter, when one is chosen: it narrows what was said too. */
  inFolder: ((cwd: string) => boolean) | null,
  includeDeviceName: boolean,
) {
  const needle = query.toLowerCase();
  const named = rows
    .filter(
      ({ session, deviceName }) =>
        session.name.toLowerCase().includes(needle) ||
        session.cwd.toLowerCase().includes(needle) ||
        (includeDeviceName && deviceName.toLowerCase().includes(needle)),
    )
    .sort((a, b) => b.session.lastActivityAt - a.session.lastActivityAt);
  const listed = new Set(named.map((row) => row.key));
  const byKey = new Map(rows.map((row) => [row.key, row]));
  const hits = new Map<string, SearchHit>();
  const others: FleetSession[] = [];
  for (const entry of found?.hits ?? []) {
    const session = entry.hit.session;
    if (!session || hits.has(entry.key)) continue;
    if (inFolder && !inFolder(session.cwd)) continue;
    hits.set(entry.key, entry.hit);
    if (!listed.has(entry.key)) {
      others.push(
        byKey.get(entry.key) ?? {
          key: entry.key,
          host: entry.host,
          deviceName: entry.deviceName,
          session,
        },
      );
    }
  }
  const sections: { label: string; tint: string; rank: number; sessions: FleetSession[] }[] = [];
  if (named.length) sections.push({ label: "Agents", tint: color.muted, rank: 0, sessions: named });
  if (others.length) sections.push({ label: "In messages", tint: color.muted, rank: 1, sessions: others });
  return { sections, hits };
}

/** A search box over the list, as Mail and Messages have one. */
function SearchField({ value, onChange }: { value: string; onChange(text: string): void }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <View style={styles.searchField}>
      <Feather name="search" size={16} color={color.muted} />
      <TextInput
        value={value}
        onChangeText={onChange}
        placeholder="Search agents and messages"
        placeholderTextColor={color.faint}
        style={styles.searchInput}
        returnKeyType="search"
        autoCorrect={false}
        autoCapitalize="none"
        accessibilityLabel="Search agents and messages"
      />
      {value ? (
        <MotionPressable hitSlop={10} onPress={() => onChange("")} pressedScale={0.9}
          accessibilityRole="button" accessibilityLabel="Clear search">
          <Feather name="x-circle" size={16} color={color.faint} />
        </MotionPressable>
      ) : null}
    </View>
  );
}

/** Under the box while searching: what is happening, or that nothing matched. */
function SearchStatus({ problem, pending, indexing, empty }: {
  problem: string | null;
  pending: boolean;
  indexing: boolean;
  empty: boolean;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const line = problem
    ? problem
    : pending
      ? "Searching messages…"
      : indexing
        ? "Your Mac is still reading older conversations, so more may turn up."
        : empty
          ? "Nothing matches."
          : null;
  if (!line) return null;
  return (
    <View style={styles.searchStatus}>
      {pending ? <ActivityIndicator size="small" color={color.faint} /> : null}
      <Text style={[styles.searchStatusText, problem ? { color: color.errorText } : null]}>{line}</Text>
    </View>
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
  label,
  online,
}: {
  label: string;
  online: boolean;
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
      <Text style={styles.chipText} numberOfLines={1}>
        {label}
      </Text>
    </View>
  );
}

/**
 * The one row of controls above the list.
 *
 * Device (when more than one is paired) and Folder on the left, then a count
 * per state. Horizontally scrollable rather than wrapped: a second line here
 * would put the first agent below the fold again, which is the thing this
 * replaced.
 */
function FilterBar({
  counts,
  ended,
  stateFilter,
  setStateFilter,
  namedHosts,
  filteredHost,
  anyOnline,
}: {
  counts: { needsYou: number; working: number; idle: number };
  ended: number;
  stateFilter: StateFilter | null;
  setStateFilter(next: StateFilter | null): void;
  namedHosts: NamedHost[];
  filteredHost: NamedHost | null;
  anyOnline: boolean;
}) {
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
  const folderHost = filteredHost ?? (namedHosts.length === 1 ? namedHosts[0] : null);

  return (
    <ScrollView
      horizontal
      showsHorizontalScrollIndicator={false}
      style={styles.filterBar}
      contentContainerStyle={styles.filterBarRow}
      keyboardShouldPersistTaps="handled"
    >
      <DeviceFilter namedHosts={namedHosts} filteredHost={filteredHost} />
      <FolderFilter host={folderHost} anyOnline={anyOnline} />
      {chips
        .filter((chip) => chip.value > 0 || stateFilter === chip.key)
        .map((chip) => {
        const on = stateFilter === chip.key;
        return (
          <MotionPressable
            key={chip.key}
            onPress={() => setStateFilter(on ? null : chip.key)}
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
      <EmptyArt sticker="heart" />
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

function OfflineBanner({
  lastSeenAt,
  subject = "Your Mac is",
}: {
  lastSeenAt: number | null;
  subject?: string;
}) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  return (
    <View style={styles.banner} accessibilityRole="alert">
      <View style={styles.bannerIcon}>
        <Feather name="wifi-off" size={15} color={color.errorText} />
      </View>
      <View style={styles.bannerCopy}>
        <Text style={styles.bannerText}>
          {subject} offline
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

/** An empty list's picture: the website's sticker on dots, or today's
 *  drawing in a card in the Classic theme. */
function EmptyArt({ sticker }: { sticker: "heart" | "notes" | "terminal" }) {
  const styles = useStyles(makeStyles);
  const { look } = useTheme();
  return look === "site" ? <StickerOnDots name={sticker} size={scaled(150)} /> : <EmptyIllustration style={styles.emptyArt} />;
}

/** A command to run on the Mac, copied with a tap, as on the pairing screen. */
function CopyCommand({ command }: { command: string }) {
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(false), 1600);
    return () => clearTimeout(timer);
  }, [copied]);
  return (
    <MotionPressable
      onPress={() => {
        void Clipboard.setStringAsync(command);
        if (Platform.OS !== "web") void Haptics.selectionAsync().catch(() => {});
        setCopied(true);
      }}
      pressedScale={0.98}
      style={styles.command}
      accessibilityRole="button"
      accessibilityLabel={`Copy ${command}`}
      accessibilityHint="Copies the command so you can paste it into a terminal"
    >
      <Text style={styles.commandPrompt}>$</Text>
      <Text style={styles.commandText}>{command}</Text>
      <Feather name={copied ? "check" : "copy"} size={15} color={copied ? color.working : color.faint} />
    </MotionPressable>
  );
}

function EmptyState({ folder }: { folder: string | null }) {
  const styles = useStyles(makeStyles);
  const { look } = useTheme();
  // A filtered folder that turns up nothing is a different fact from having
  // no agents at all, and the advice for it is different too: the fix is to
  // widen the filter, not to open a terminal.
  if (folder) {
    return (
      <View style={styles.empty}>
        <EmptyArt sticker="notes" />
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
      <EmptyArt sticker="terminal" />
      <Text style={styles.emptyTitle}>Nothing running</Text>
      <Text style={styles.emptyBody}>
        Tap + to start an agent in a folder on your Mac, or start one from its
        terminal. Cursor IDE sessions are view-only.
      </Text>
      {look === "site" ? (
        <CopyCommand command="am claude" />
      ) : (
        <View style={styles.command}>
          <Text style={styles.commandPrompt}>$</Text>
          <Text style={styles.commandText} selectable>
            am claude
          </Text>
        </View>
      )}
      <Text style={styles.emptyHint}>
        Also <Text style={styles.inlineMono}>am codex</Text> and{" "}
        <Text style={styles.inlineMono}>am opencode</Text>. Recent Cursor IDE
        chats appear automatically in read-only mode.
      </Text>
    </View>
  );
}

const makeSiteStyles = (c: Palette) =>
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
      maxWidth: "70%",
      flexShrink: 1,
      paddingHorizontal: space.md,
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      ...edged(c),
    },
    chipDot: { width: 7, height: 7, borderRadius: 4, flexShrink: 0 },
    chipText: {
      flexShrink: 1,
      ...font.sansMedium,
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
      ...edged(c, 2),
    },
    iconButtonFlood: { backgroundColor: c.flood },
    title: {
      ...font.display,
      fontSize: size.display,
      letterSpacing: -size.display * 0.03,
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
      ...edged(c),
    },
    stateChipOn: { backgroundColor: c.inverse },
    stateChipValue: {
      ...font.sansBold,
      fontSize: size.caption,
      fontVariant: ["tabular-nums"],
    },
    stateChipLabel: {
      ...font.sans,
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
      ...edged(c),
    },
    filterOn: { backgroundColor: c.cream },
    filterLabel: {
      flexShrink: 1,
      ...font.sans,
      fontSize: size.body,
      color: c.textSecondary,
    },
    filterLabelOn: {
      ...font.monoMedium,
      fontSize: size.caption,
      color: c.text,
    },
    deviceFilterLabelOn: {
      ...font.sansMedium,
      fontSize: size.caption,
      color: c.text,
    },
    filterClear: {
      width: 40,
      height: 40,
      alignItems: "center",
      justifyContent: "center",
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      ...edged(c),
    },
    groupHeading: {
      flexDirection: "row",
      alignItems: "baseline",
      justifyContent: "space-between",
      paddingHorizontal: space.xxs,
      marginTop: space.xl,
      marginBottom: space.sm,
    },
    groupLabel: { ...eyebrow(c) },
    groupCount: { ...font.monoMedium, fontSize: size.label, color: c.faint },

    card: {
      borderRadius: radius.xl,
      backgroundColor: c.surface,
      ...edged(c, 3),
      marginBottom: ROW_GAP + 3,
    },
    // A question waiting on you: its shadow is the needs-you orange.
    askCard: {
      padding: space.lg,
      gap: space.md,
      ...hardShadow(c.needsYou, 4),
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
      ...font.sansBold,
      fontSize: scaled(11.5),
      color: c.needsYouText,
    },
    askName: {
      flex: 1,
      ...font.sansMedium,
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
    rowNeedsYou: { backgroundColor: c.needsYouWash },
    groupRow: {
      backgroundColor: c.surface,
      borderLeftWidth: 1.5,
      borderRightWidth: 1.5,
      borderColor: c.edge,
    },
    groupFirst: {
      borderTopWidth: 1.5,
      borderTopLeftRadius: radius.xl,
      borderTopRightRadius: radius.xl,
    },
    // The group's last row carries its hard shadow, so it shows under the
    // whole group.
    groupLast: {
      borderBottomWidth: 1.5,
      borderBottomLeftRadius: radius.xl,
      borderBottomRightRadius: radius.xl,
      ...hardShadow(c.edge, 3),
      marginBottom: ROW_GAP + 3,
    },
    groupDivider: {
      borderBottomWidth: StyleSheet.hairlineWidth,
      borderBottomColor: c.line,
    },
    avatar: {
      width: 36,
      height: 36,
      borderRadius: radius.md,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.fill,
    },
    serversLine: { flexDirection: "row", alignItems: "center", gap: 5 },
    serversText: { ...font.mono, fontSize: scaled(12), color: c.muted },
    deviceLine: { flexDirection: "row", alignItems: "center", gap: 5, marginTop: 1 },
    deviceText: { flexShrink: 1, ...font.sansMedium, fontSize: size.label, color: c.faint },
    avatarLoader: {
      position: "absolute",
      right: -5,
      bottom: -5,
      width: 18,
      height: 18,
      borderRadius: 9,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.surface,
    },
    avatarPulse: {
      position: "absolute",
      right: -8,
      bottom: -8,
    },
    rowBody: { flex: 1, gap: 3 },
    rowTop: { flexDirection: "row", alignItems: "baseline", gap: space.sm },
    name: {
      flex: 1,
      ...font.sansMedium,
      fontSize: size.body,
      color: c.text,
    },
    age: { ...font.sans, fontSize: size.label, color: c.faint },
    metaRow: { flexDirection: "row", alignItems: "center" },
    // The model holds its width and the path gives way from its front: a
    // path's last segment is the one that says which project this is.
    metaPath: { flexShrink: 1 },
    meta: { ...font.sans, fontSize: size.caption, color: c.muted },
    snippet: {
      ...font.sans,
      fontSize: size.caption,
      lineHeight: scaled(18),
      color: c.muted,
      marginTop: space.xs,
    },
    snippetMatch: { ...font.sansBold, color: c.text },
    searchField: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      minHeight: 42,
      marginTop: space.md,
      paddingHorizontal: space.md,
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      ...edged(c),
    },
    searchInput: {
      flex: 1,
      ...font.sans,
      fontSize: size.body,
      color: c.text,
      paddingVertical: space.sm,
    },
    searchStatus: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      marginTop: space.lg,
      paddingHorizontal: space.xxs,
    },
    searchStatusText: { ...font.sans, fontSize: size.caption, color: c.muted, flexShrink: 1 },
    limitLine: { flexDirection: "row", alignItems: "center", gap: 5 },
    limitNote: { flexShrink: 1, ...font.sansMedium, fontSize: size.caption, color: c.errorText },
    needsYouNote: {
      ...font.sansMedium,
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
      ...font.sansBold,
      fontSize: size.caption,
      color: c.text,
    },
    bannerHint: {
      ...font.sans,
      fontSize: size.caption,
      color: c.muted,
      marginTop: 2,
    },

    empty: {
      marginTop: space.xl,
      alignItems: "center",
      gap: space.sm,
    },
    emptyTitle: {
      ...font.displayBold,
      fontSize: scaled(28),
      letterSpacing: -scaled(28) * 0.02,
      color: c.text,
      textAlign: "center",
    },
    emptyBody: {
      ...font.sans,
      fontSize: size.body,
      color: c.muted,
      lineHeight: scaled(22),
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
      borderRadius: 14,
      backgroundColor: c.surface,
      borderWidth: 1.5,
      borderColor: c.edge,
      ...hardShadow(c.edge),
    },
    commandPrompt: { ...font.monoMedium, fontSize: scaled(14), color: c.prompt },
    commandText: { flex: 1, ...font.mono, fontSize: scaled(14), color: c.text },
    emptyHint: {
      ...font.sans,
      fontSize: size.caption,
      color: c.faint,
      marginTop: space.xs,
    },
    inlineMono: { ...font.mono, color: c.muted },
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
      ...font.sansBold,
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
      ...font.sans,
      fontSize: size.body,
      color: c.onInverse,
    },
    undoAction: {
      ...font.sansBold,
      fontSize: size.body,
      color: c.inverseAccent,
    },
    undoButton: { paddingVertical: space.xs, paddingHorizontal: space.sm },
  });

const makeClassicStyles = (c: Palette) =>
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
      maxWidth: "70%",
      flexShrink: 1,
      paddingHorizontal: space.md,
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
    },
    chipDot: { width: 7, height: 7, borderRadius: 4, flexShrink: 0 },
    chipText: {
      flexShrink: 1,
      ...font.sansMedium,
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
      ...font.sansBold,
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
      ...font.sansBold,
      fontSize: size.caption,
      fontVariant: ["tabular-nums"],
    },
    stateChipLabel: {
      ...font.sans,
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
      ...font.sans,
      fontSize: size.body,
      color: c.textSecondary,
    },
    filterLabelOn: {
      ...font.monoMedium,
      fontSize: size.caption,
      color: c.workingText,
    },
    deviceFilterLabelOn: {
      ...font.sansMedium,
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
    groupLabel: { ...font.sansBold, fontSize: size.caption },
    groupCount: { ...font.mono, fontSize: size.label, color: c.faint },

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
      ...font.sansBold,
      fontSize: scaled(11.5),
      color: c.needsYouText,
    },
    askName: {
      flex: 1,
      ...font.sansMedium,
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
    groupRow: {
      backgroundColor: c.surface,
      borderLeftWidth: 1,
      borderRightWidth: 1,
      borderColor: c.line,
    },
    groupFirst: {
      borderTopWidth: 1,
      borderTopLeftRadius: radius.xl,
      borderTopRightRadius: radius.xl,
    },
    groupLast: {
      borderBottomWidth: 1,
      borderBottomLeftRadius: radius.xl,
      borderBottomRightRadius: radius.xl,
      marginBottom: ROW_GAP,
    },
    groupDivider: {
      borderBottomWidth: StyleSheet.hairlineWidth,
      borderBottomColor: c.line,
    },
    avatar: {
      width: 36,
      height: 36,
      borderRadius: radius.md,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.fill,
    },
    serversLine: { flexDirection: "row", alignItems: "center", gap: 5 },
    serversText: { ...font.mono, fontSize: scaled(12), color: c.muted },
    deviceLine: { flexDirection: "row", alignItems: "center", gap: 5, marginTop: 1 },
    deviceText: { flexShrink: 1, ...font.sansMedium, fontSize: size.label, color: c.faint },
    avatarLoader: {
      position: "absolute",
      right: -5,
      bottom: -5,
      width: 18,
      height: 18,
      borderRadius: 9,
      alignItems: "center",
      justifyContent: "center",
      backgroundColor: c.surface,
    },
    avatarPulse: {
      position: "absolute",
      right: -8,
      bottom: -8,
    },
    rowBody: { flex: 1, gap: 3 },
    rowTop: { flexDirection: "row", alignItems: "baseline", gap: space.sm },
    name: {
      flex: 1,
      ...font.sansMedium,
      fontSize: size.body,
      color: c.text,
    },
    age: { ...font.sans, fontSize: size.label, color: c.faint },
    metaRow: { flexDirection: "row", alignItems: "center" },
    // The model holds its width and the path gives way from its front: a
    // path's last segment is the one that says which project this is.
    metaPath: { flexShrink: 1 },
    meta: { ...font.sans, fontSize: size.caption, color: c.muted },
    snippet: {
      ...font.sans,
      fontSize: size.caption,
      lineHeight: scaled(18),
      color: c.muted,
      marginTop: space.xs,
    },
    snippetMatch: { ...font.sansBold, color: c.text },
    searchField: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      minHeight: 42,
      marginTop: space.md,
      paddingHorizontal: space.md,
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      borderWidth: StyleSheet.hairlineWidth,
      borderColor: c.fillStrong,
    },
    searchInput: {
      flex: 1,
      ...font.sans,
      fontSize: size.body,
      color: c.text,
      paddingVertical: space.sm,
    },
    searchStatus: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      marginTop: space.lg,
      paddingHorizontal: space.xxs,
    },
    searchStatusText: { ...font.sans, fontSize: size.caption, color: c.muted, flexShrink: 1 },
    limitLine: { flexDirection: "row", alignItems: "center", gap: 5 },
    limitNote: { flexShrink: 1, ...font.sansMedium, fontSize: size.caption, color: c.errorText },
    needsYouNote: {
      ...font.sansMedium,
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
      ...font.sansBold,
      fontSize: size.caption,
      color: c.text,
    },
    bannerHint: {
      ...font.sans,
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
      ...font.sansBold,
      fontSize: scaled(26),
      letterSpacing: -0.8,
      color: c.text,
      textAlign: "center",
    },
    emptyBody: {
      ...font.sans,
      fontSize: size.body,
      color: c.muted,
      lineHeight: scaled(22),
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
    commandPrompt: { ...font.mono, fontSize: scaled(14), color: c.faint },
    commandText: { ...font.mono, fontSize: scaled(14), color: c.text },
    emptyHint: {
      ...font.sans,
      fontSize: size.caption,
      color: c.faint,
      marginTop: space.xs,
    },
    inlineMono: { ...font.mono, color: c.muted },
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
      ...font.sansBold,
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
      ...font.sans,
      fontSize: size.body,
      color: c.onInverse,
    },
    undoAction: {
      ...font.sansBold,
      fontSize: size.body,
      color: c.inverseAccent,
    },
    undoButton: { paddingVertical: space.xs, paddingHorizontal: space.sm },
  });

// Today's styles for the Classic theme, the website's look's above them
// (see lib/theme: a palette's look). Keys only the new look has keep its
// values, for what only the new look shows.
type Styles = ReturnType<typeof makeSiteStyles> & ReturnType<typeof makeClassicStyles>;
const makeStyles = (c: Palette): Styles =>
  (c.look === "site" ? makeSiteStyles(c) : { ...makeSiteStyles(c), ...makeClassicStyles(c) }) as Styles;

