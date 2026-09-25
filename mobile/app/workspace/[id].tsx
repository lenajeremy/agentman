import Feather from "@expo/vector-icons/Feather";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { ContentColumn } from "../../components/ContentColumn";
import { FileBadge } from "../../components/FileBadge";
import { MotionPressable } from "../../components/MotionPressable";
import { useStyles, useTheme } from "../../lib/appearance";
import { WorkspaceResult } from "../../lib/protocol";
import { useStore } from "../../lib/store";
import { font, Palette, radius, size, space } from "../../lib/theme";

/** A directory listing is worth filtering long before it is worth paging. */
const FILTER_FROM_ENTRIES = 8;

export default function WorkspaceScreen() {
  const {
    id,
    path: pathParam,
    tab: tabParam,
  } = useLocalSearchParams<{
    id: string;
    path?: string;
    tab?: string;
  }>();
  const sessionId = decodeURIComponent(String(id));
  // Folders are local state rather than routes. Pushing a screen per level
  // rebuilt the header and tabs on every tap and made "back" walk out one
  // directory at a time; this keeps one page whose listing changes.
  const [folder, setFolder] = useState(
    typeof pathParam === "string" ? pathParam : "",
  );
  const store = useStore();
  const session = store.sessions.find((item) => item.id === sessionId);
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const styles = useStyles(makeStyles);
  const { color } = useTheme();
  // Openable on either tab. Arriving from "3 files changed" and landing on a
  // directory listing means tapping again to reach the thing that was named on
  // the button you pressed.
  const [tab, setTab] = useState<"files" | "changes">(
    tabParam === "changes" ? "changes" : "files",
  );
  const [result, setResult] = useState<WorkspaceResult | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [refresh, setRefresh] = useState(0);
  const [filter, setFilter] = useState("");

  useEffect(() => {
    let current = true;
    setLoading(true);
    setError("");
    setResult(null);
    void store
      .workspace(
        sessionId,
        tab === "files" ? "list_files" : "list_changes",
        tab === "files" ? folder : "",
      )
      .then((value) => {
        if (current) setResult(value);
      })
      .catch((reason) => {
        if (current)
          setError(reason instanceof Error ? reason.message : String(reason));
      })
      .finally(() => {
        if (current) setLoading(false);
      });
    return () => {
      current = false;
    };
    // The request is keyed to route and refresh, not to store's changing snapshot.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, folder, tab, refresh]);

  // A filter typed in one folder means nothing in the next one.
  useEffect(() => setFilter(""), [folder, tab]);

  const openFolder = (name: string) =>
    setFolder(folder ? `${folder}/${name}` : name);
  const openFile = (name: string, initial: "code" | "diff" = "code") => {
    const next = tab === "files" && folder ? `${folder}/${name}` : name;
    router.push(
      `/file/${encodeURIComponent(sessionId)}?path=${encodeURIComponent(next)}&tab=${initial}`,
    );
  };
  /** Jump straight to an ancestor instead of stepping up one level at a time. */
  const openCrumb = (depth: number) =>
    setFolder(folder.split("/").slice(0, depth).join("/"));
  // Back leaves the workspace only from the root; inside, it goes up a level,
  // which is what the breadcrumb implies.
  const goBack = () => {
    if (folder) openCrumb(folder.split("/").length - 1);
    else router.back();
  };

  const changeLabel = (status: string) => {
    if (status === "??") return "New";
    if (status.includes("U")) return "Conflict";
    if (status.includes("D")) return "Deleted";
    if (status.includes("A")) return "Added";
    if (status.includes("R")) return "Renamed";
    return "Modified";
  };

  const needle = filter.trim().toLowerCase();
  const entries = useMemo(() => {
    const all = result?.entries ?? [];
    if (!needle) return all;
    return all.filter((entry) => entry.name.toLowerCase().includes(needle));
  }, [result, needle]);
  const changes = useMemo(() => {
    const all = result?.changes ?? [];
    if (!needle) return all;
    return all.filter((change) => change.path.toLowerCase().includes(needle));
  }, [result, needle]);

  const crumbs = folder ? folder.split("/") : [];
  const total =
    tab === "files"
      ? (result?.entries?.length ?? 0)
      : (result?.changes?.length ?? 0);
  const hidden = result?.hidden ?? 0;
  const hiddenLabel = `${hidden} ${hidden === 1 ? "file" : "files"}`;
  const showFilter = !loading && !error && total >= FILTER_FROM_ENTRIES;

  return (
    <View style={[styles.page, { paddingTop: insets.top }]}>
      <ContentColumn style={styles.header}>
        <View style={styles.topBar}>
          <MotionPressable
            onPress={goBack}
            style={styles.iconButton}
            accessibilityRole="button"
            accessibilityLabel={folder ? "Up one folder" : "Back"}
          >
            <Feather name="chevron-left" size={20} color={color.text} />
          </MotionPressable>
          <View style={styles.heading}>
            <Text
              style={styles.title}
              numberOfLines={1}
              accessibilityRole="header"
            >
              {tab === "changes"
                ? "Changes"
                : crumbs.length > 0
                  ? crumbs[crumbs.length - 1]
                  : "Workspace"}
            </Text>
            <Text style={styles.subtitle} numberOfLines={1}>
              {session ? session.cwd.split("/").at(-1) : sessionId}
              {total > 0
                ? ` · ${total}${result?.truncated ? "+" : ""} item${total === 1 ? "" : "s"}`
                : ""}
            </Text>
          </View>
          <MotionPressable
            onPress={() => setRefresh((value) => value + 1)}
            style={styles.iconButton}
            accessibilityRole="button"
            accessibilityLabel="Refresh"
          >
            <Feather name="refresh-cw" size={17} color={color.text} />
          </MotionPressable>
        </View>

        <View style={styles.tabs}>
          {(["files", "changes"] as const).map((value) => (
            <MotionPressable
              key={value}
              onPress={() => setTab(value)}
              style={[styles.tab, tab === value && styles.tabActive]}
              accessibilityRole="tab"
              accessibilityState={{ selected: tab === value }}
            >
              <Text
                style={[styles.tabText, tab === value && styles.tabTextActive]}
              >
                {value === "files" ? "Files" : "Changes"}
              </Text>
            </MotionPressable>
          ))}
        </View>

        {tab === "files" ? (
          // Breadcrumbs scroll rather than wrap: a deep path must not push the
          // listing down the screen.
          <ScrollView
            horizontal
            showsHorizontalScrollIndicator={false}
            style={styles.crumbBar}
            contentContainerStyle={styles.crumbs}
          >
            <MotionPressable
              onPress={() => openCrumb(0)}
              disabled={crumbs.length === 0}
              accessibilityRole="button"
              accessibilityLabel="Workspace root"
            >
              <Feather
                name="home"
                size={13}
                color={crumbs.length === 0 ? color.text : color.workingText}
              />
            </MotionPressable>
            {crumbs.map((crumb, index) => (
              <View key={`${crumb}:${index}`} style={styles.crumbItem}>
                <Feather name="chevron-right" size={12} color={color.faint} />
                <MotionPressable
                  onPress={() => openCrumb(index + 1)}
                  disabled={index === crumbs.length - 1}
                  accessibilityRole="button"
                >
                  <Text
                    style={[
                      styles.crumb,
                      index === crumbs.length - 1 && styles.crumbCurrent,
                    ]}
                    numberOfLines={1}
                  >
                    {crumb}
                  </Text>
                </MotionPressable>
              </View>
            ))}
          </ScrollView>
        ) : null}

        {showFilter ? (
          <View style={styles.search}>
            <Feather name="search" size={14} color={color.faint} />
            <View style={styles.searchField}>
              {/* The native placeholder rendered in the wrong face — tracked
                  out, as though it had fallen back to the mono font — while
                  every other Text in the app resolved Geist correctly. Drawing
                  it as a Text makes it match the rest by construction. */}
              {filter.length === 0 ? (
                <Text
                  style={styles.searchPlaceholder}
                  pointerEvents="none"
                  numberOfLines={1}
                >
                  {tab === "files" ? "Filter files…" : "Filter changes…"}
                </Text>
              ) : null}
              <TextInput
                value={filter}
                onChangeText={setFilter}
                style={styles.searchInput}
                autoCapitalize="none"
                autoCorrect={false}
                accessibilityLabel={
                  tab === "files" ? "Filter files" : "Filter changes"
                }
              />
            </View>
            {filter.length > 0 ? (
              <MotionPressable
                onPress={() => setFilter("")}
                hitSlop={8}
                accessibilityRole="button"
                accessibilityLabel="Clear filter"
              >
                <Feather name="x-circle" size={15} color={color.faint} />
              </MotionPressable>
            ) : null}
          </View>
        ) : null}
      </ContentColumn>

      <ScrollView
        contentContainerStyle={{ paddingBottom: insets.bottom + space.xl }}
        keyboardShouldPersistTaps="handled"
      >
        <ContentColumn style={styles.content}>
          {loading && (
            <ActivityIndicator style={styles.center} color={color.muted} />
          )}
          {!!error && <Text style={styles.error}>{error}</Text>}

          {!loading &&
            !error &&
            needle.length > 0 &&
            entries.length === 0 &&
            changes.length === 0 && (
              <View style={styles.emptyWrap}>
                <Feather name="search" size={22} color={color.faint} />
                <Text style={styles.empty}>
                  Nothing matches “{filter.trim()}”.
                </Text>
              </View>
            )}
          {!loading && !error && !needle && tab === "files" && total === 0 && (
            <View style={styles.emptyWrap}>
              <Feather name="folder" size={22} color={color.faint} />
              <Text style={styles.empty}>
                {hidden > 0
                  ? `Nothing to show here. ${hiddenLabel} kept private.`
                  : "This folder is empty."}
              </Text>
            </View>
          )}
          {!loading &&
            !error &&
            !needle &&
            tab === "changes" &&
            total === 0 && (
              <View style={styles.emptyWrap}>
                <Feather name="check-circle" size={22} color={color.faint} />
                <Text style={styles.empty}>
                  {hidden > 0
                    ? `No changes you can review. ${hiddenLabel} kept private.`
                    : "No working tree changes here."}
                </Text>
              </View>
            )}

          {/* One bordered surface with hairline dividers, rather than a card
              per row: a directory is a list, and the per-row chrome was
              costing about half the rows that fit on screen. */}
          {tab === "files" && entries.length > 0 ? (
            <View style={styles.list}>
              {entries.map((entry, index) => (
                <MotionPressable
                  key={entry.name}
                  onPress={() =>
                    entry.directory
                      ? openFolder(entry.name)
                      : openFile(entry.name)
                  }
                  style={[styles.row, index > 0 && styles.rowDivided]}
                  pressedScale={0.995}
                  accessibilityRole="button"
                  accessibilityLabel={`${entry.name}, ${entry.directory ? "folder" : "file"}`}
                >
                  <FileBadge name={entry.name} directory={entry.directory} />
                  <Text
                    style={[
                      styles.rowName,
                      entry.directory && styles.rowFolder,
                    ]}
                    numberOfLines={1}
                  >
                    {entry.name}
                  </Text>
                  {!entry.directory && entry.size !== undefined ? (
                    <Text style={styles.rowMeta}>{formatSize(entry.size)}</Text>
                  ) : null}
                  <Feather name="chevron-right" size={14} color={color.faint} />
                </MotionPressable>
              ))}
            </View>
          ) : null}

          {tab === "changes" && changes.length > 0 ? (
            <View style={styles.list}>
              {changes.map((change, index) => (
                <MotionPressable
                  key={change.path}
                  onPress={() => openFile(change.path, "diff")}
                  style={[
                    styles.row,
                    styles.changeRow,
                    index > 0 && styles.rowDivided,
                  ]}
                  pressedScale={0.995}
                  accessibilityRole="button"
                  accessibilityLabel={`${change.path}, ${changeLabel(change.status)}`}
                >
                  <FileBadge name={change.path} directory={false} />
                  <View style={styles.changeIdentity}>
                    <Text style={styles.rowName} numberOfLines={1}>
                      {change.path.split("/").at(-1)}
                    </Text>
                    {change.path.includes("/") ? (
                      <Text style={styles.changeDir} numberOfLines={1}>
                        {change.path.split("/").slice(0, -1).join("/")}
                      </Text>
                    ) : null}
                  </View>
                  <ChangeCount change={change} styles={styles} />
                  <Feather name="chevron-right" size={14} color={color.faint} />
                </MotionPressable>
              ))}
            </View>
          ) : null}

          {result?.truncated && (
            <Text style={styles.note}>Showing the first entries only.</Text>
          )}
          {/* Say what was withheld. This screen once reported "no changes"
              while twenty files sat behind a filter, and nothing on it could
              have told you otherwise. */}
          {hidden > 0 && total > 0 && (
            <Text style={styles.note}>
              {hiddenLabel} kept private and not shown.
            </Text>
          )}
          {!loading && !error && (
            <Text style={styles.note}>
              {tab === "files"
                ? "Private files and symlinks are hidden. Files are read only."
                : "Changes since the last commit, including staged and new files."}
            </Text>
          )}
        </ContentColumn>
      </ScrollView>
    </View>
  );
}

/**
 * How much a file changed, rather than that it changed.
 *
 * "Modified" is the same word for a typo and a rewrite. Counts answer the
 * question the word raises, and they are colour-coded the way a diff already
 * is: green where a file only grew, amber where it was edited in place, red
 * where it went away. Binary files report no counts, so they keep a word.
 */
function ChangeCount({
  change,
  styles,
}: {
  change: { status: string; added?: number; removed?: number };
  styles: ReturnType<typeof makeStyles>;
}) {
  const added = change.added ?? 0;
  const removed = change.removed ?? 0;
  if (change.status.includes("D"))
    return <Text style={[styles.count, styles.countRemoved]}>deleted</Text>;
  if (change.status.includes("U"))
    return <Text style={[styles.count, styles.countConflict]}>conflict</Text>;
  if (added === 0 && removed === 0) {
    return (
      <Text style={[styles.count, styles.countNeutral]}>
        {change.status === "??" ? "new" : "changed"}
      </Text>
    );
  }
  return (
    <View style={styles.counts}>
      {added > 0 ? (
        <Text
          style={[
            styles.count,
            removed > 0 ? styles.countEdited : styles.countAdded,
          ]}
        >
          +{added}
        </Text>
      ) : null}
      {removed > 0 ? (
        <Text style={[styles.count, styles.countRemoved]}>−{removed}</Text>
      ) : null}
    </View>
  );
}

/** Short enough to sit in a row without pushing the filename out. */
function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

const makeStyles = (c: Palette) =>
  StyleSheet.create({
    page: { flex: 1, backgroundColor: c.paper },
    header: {
      paddingHorizontal: space.lg,
      paddingTop: space.sm,
      paddingBottom: space.sm,
    },
    topBar: { flexDirection: "row", alignItems: "center", gap: space.sm },
    heading: { flex: 1, minWidth: 0 },
    // The old display-size title cost a fifth of the screen on a browser whose
    // whole job is showing as many rows as possible.
    title: {
      color: c.text,
      fontFamily: font.sansBold,
      fontSize: size.title,
      letterSpacing: -0.3,
    },
    subtitle: {
      color: c.muted,
      fontFamily: font.mono,
      fontSize: 11,
      marginTop: 1,
    },
    iconButton: {
      width: 36,
      height: 36,
      alignItems: "center",
      justifyContent: "center",
      borderRadius: radius.pill,
      backgroundColor: c.surface,
      borderWidth: 1,
      borderColor: c.line,
    },

    tabs: { flexDirection: "row", gap: space.xs, marginTop: space.md },
    tab: {
      paddingHorizontal: space.lg,
      paddingVertical: 7,
      borderRadius: radius.pill,
      backgroundColor: c.fill,
    },
    tabActive: { backgroundColor: c.inverse },
    tabText: {
      color: c.muted,
      fontFamily: font.sansMedium,
      fontSize: size.caption,
    },
    tabTextActive: { color: c.onInverse },

    crumbBar: { marginTop: space.md, flexGrow: 0 },
    crumbs: {
      flexDirection: "row",
      alignItems: "center",
      gap: 3,
      paddingRight: space.lg,
    },
    crumbItem: { flexDirection: "row", alignItems: "center", gap: 3 },
    crumb: { color: c.workingText, fontFamily: font.mono, fontSize: 12 },
    crumbCurrent: { color: c.text, fontFamily: font.monoMedium },

    search: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.sm,
      marginTop: space.md,
      height: 40,
      paddingHorizontal: space.md,
      borderRadius: radius.md,
      backgroundColor: c.fill,
    },
    // The field fills the row and centres its own text. `padding: 0` alone left
    // the placeholder sitting off the row's centre line, and without an explicit
    // lineHeight the two platforms disagreed about where the baseline sits.
    searchField: { flex: 1, height: "100%", justifyContent: "center" },
    searchPlaceholder: {
      position: "absolute",
      left: 0,
      right: 0,
      color: c.faint,
      fontFamily: font.sans,
      fontSize: size.caption,
    },
    searchInput: {
      height: "100%",
      color: c.text,
      fontFamily: font.sans,
      fontSize: size.caption,
      lineHeight: 18,
      paddingVertical: 0,
      paddingHorizontal: 0,
      includeFontPadding: false,
      textAlignVertical: "center",
    },

    content: { paddingHorizontal: space.lg, paddingTop: space.sm },
    list: {
      borderWidth: 1,
      borderColor: c.line,
      borderRadius: radius.lg,
      backgroundColor: c.surface,
      overflow: "hidden",
    },
    row: {
      flexDirection: "row",
      alignItems: "center",
      gap: space.md,
      minHeight: 44,
      paddingHorizontal: space.md,
    },
    rowDivided: {
      borderTopWidth: StyleSheet.hairlineWidth,
      borderTopColor: c.line,
    },
    rowName: { flex: 1, color: c.text, fontFamily: font.mono, fontSize: 13 },
    rowFolder: { fontFamily: font.monoMedium },
    rowMeta: { color: c.faint, fontFamily: font.mono, fontSize: 11 },

    // Two lines need room to breathe: at the file rows' 44pt the name and its
    // directory were pressed against the dividers above and below.
    changeRow: {
      minHeight: 56,
      paddingVertical: space.sm,
      alignItems: "center",
    },
    // justifyContent keeps a root-level file — one with no directory beneath it
    // — on the row's centre line instead of sitting where the first of two
    // lines would. The gap is small because the path is a continuation of the
    // name, not a separate field.
    changeIdentity: { flex: 1, minWidth: 0, gap: 1, justifyContent: "center" },
    changeDir: { color: c.faint, fontFamily: font.mono, fontSize: 10.5 },
    counts: { flexDirection: "row", alignItems: "center", gap: 6 },
    count: { fontFamily: font.monoMedium, fontSize: 11.5 },
    countAdded: { color: c.ok },
    // Amber, not blue: an edited file sits between wholly new and deleted, and
    // the palette's "working" blue already means an agent is running.
    countEdited: { color: "#B07A16" },
    countRemoved: { color: c.errorText },
    countConflict: { color: c.needsYouText },
    countNeutral: { color: c.faint },

    emptyWrap: {
      alignItems: "center",
      gap: space.sm,
      paddingVertical: space.xxl,
    },
    empty: {
      color: c.muted,
      fontFamily: font.sans,
      fontSize: size.caption,
      textAlign: "center",
    },
    error: {
      color: c.errorText,
      fontFamily: font.sans,
      fontSize: size.body,
      paddingVertical: space.md,
    },
    center: { marginTop: space.xl },
    note: {
      color: c.faint,
      fontFamily: font.sans,
      fontSize: 11.5,
      marginTop: space.md,
      lineHeight: 16,
    },
  });
